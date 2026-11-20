package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const (
	ModelProbeMaxModels       = 3
	ModelProbeMaxEndpoints    = 3
	ModelProbeMaxOutputTokens = 8
	ModelProbeAttemptTimeout  = 12 * time.Second
	ModelProbeBatchTimeout    = 2 * time.Minute
)

var ErrModelProbeBusy = errors.New("A model test is already running. Please wait.")

// ModelProbeRequest deliberately accepts no URL, credential, prompt, token
// budget, retry option or arbitrary provider endpoint.
type ModelProbeRequest struct {
	Models      []string `json:"models"`
	EndpointIDs []int    `json:"endpoint_ids"`
}

func (request *ModelProbeRequest) Validate() error {
	if len(request.Models) < 1 || len(request.Models) > ModelProbeMaxModels || len(request.EndpointIDs) > ModelProbeMaxEndpoints {
		return errors.New("Select one to three models and at most three endpoints")
	}
	models := map[string]bool{}
	for i, name := range request.Models {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 200 || strings.ContainsAny(name, "\r\n\x00,") || models[name] {
			return errors.New("Invalid or duplicate model ID")
		}
		models[name], request.Models[i] = true, name
	}
	endpoints := map[int]bool{}
	for _, id := range request.EndpointIDs {
		if id < 0 || endpoints[id] {
			return errors.New("Invalid or duplicate endpoint ID")
		}
		endpoints[id] = true
	}
	if len(request.EndpointIDs) == 0 {
		request.EndpointIDs = []int{0}
	}
	if len(request.EndpointIDs) > 1 && endpoints[0] {
		return errors.New("Configured egress cannot be mixed with endpoint samples")
	}
	return nil
}

type ModelProbeOutcome struct {
	Status     string
	Reason     string
	HTTPStatus int
}

func ClassifyModelProbeError(ctx context.Context, apiError *types.NewAPIError) ModelProbeOutcome {
	if errors.Is(ctx.Err(), context.Canceled) {
		return ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "cancelled"}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "timeout"}
	}
	if apiError == nil {
		return ModelProbeOutcome{Status: model.ModelEndpointAvailable, Reason: "passed", HTTPStatus: http.StatusOK}
	}
	outcome := ModelProbeOutcome{Status: model.ModelEndpointUnknown, Reason: "local_rejection", HTTPStatus: apiError.StatusCode}
	switch apiError.GetErrorCode() {
	case types.ErrorCodeInsufficientUserQuota:
		outcome.Reason = "quota_exhausted"
		return outcome
	case types.ErrorCodeDoRequestFailed:
		outcome.Reason = "transport_error"
		return outcome
	case types.ErrorCodeModelPriceError, types.ErrorCodeCountTokenFailed, types.ErrorCodeInvalidRequest, types.ErrorCodeConvertRequestFailed:
		return outcome
	}
	switch apiError.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		outcome.Status, outcome.Reason = model.ModelEndpointUnavailable, "access_denied"
	case http.StatusTooManyRequests:
		outcome.Reason = "rate_limited"
	case http.StatusNotFound:
		outcome.Status, outcome.Reason = model.ModelEndpointUnavailable, "model_not_found"
	default:
		if apiError.StatusCode >= 500 {
			outcome.Status, outcome.Reason = model.ModelEndpointUnavailable, "upstream_error"
		}
	}
	return outcome
}

var modelProbeLeases sync.Map

// AcquireModelProbe admits one batch per user across instances when Redis is
// enabled, failing closed on a store outage. It does not schedule any work.
func AcquireModelProbe(ctx context.Context, userID int) (func(), error) {
	if userID <= 0 {
		return nil, errors.New("An active dashboard session is required")
	}
	if common.RedisEnabled {
		if common.RDB == nil {
			return nil, errors.New("Model test coordination is unavailable")
		}
		key, token := fmt.Sprintf("model-probe:lease:%d", userID), common.NewRequestId()
		ok, err := common.RDB.SetNX(ctx, key, token, ModelProbeBatchTimeout+30*time.Second).Result()
		if err != nil {
			return nil, errors.New("Model test coordination is unavailable")
		}
		if !ok {
			return nil, ErrModelProbeBusy
		}
		return func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = common.RDB.Eval(releaseCtx, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{key}, token).Err()
		}, nil
	}
	if _, loaded := modelProbeLeases.LoadOrStore(userID, struct{}{}); loaded {
		return nil, ErrModelProbeBusy
	}
	return func() { modelProbeLeases.Delete(userID) }, nil
}

type ModelProbeExecutor func(context.Context, *model.Channel, string, model.ModelProbeEndpoint) ModelProbeOutcome
type ModelProbeAuthorize func(context.Context) (*model.Channel, error)

type ModelProbeBatch struct {
	ChannelID    int                         `json:"channel_id"`
	Results      []model.ModelEndpointHealth `json:"results"`
	PassedModels []string                    `json:"passed_models"`
}

// RunModelEndpointProbes runs only the explicitly selected Cartesian sample.
// Admission/quota/429 errors stop the whole batch, not just one exit, so sampling
// cannot become a mechanism to evade an upstream's access or quota decision.
func RunModelEndpointProbes(ctx context.Context, userID int, request ModelProbeRequest, authorize ModelProbeAuthorize, execute ModelProbeExecutor) (ModelProbeBatch, error) {
	batch := ModelProbeBatch{Results: []model.ModelEndpointHealth{}, PassedModels: []string{}}
	if err := request.Validate(); err != nil {
		return batch, err
	}
	stopped := ""
	passes := map[string]int{}
	for _, name := range request.Models {
		for _, endpointID := range request.EndpointIDs {
			if stopped != "" || ctx.Err() != nil {
				reason := stopped
				if reason == "" {
					reason = "cancelled"
				}
				batch.Results = append(batch.Results, model.ModelEndpointHealth{ChannelID: batch.ChannelID, Model: name, EndpointID: endpointID, Status: model.ModelEndpointUnknown, Reason: reason})
				continue
			}
			channel, err := authorize(ctx)
			if err != nil {
				return batch, err
			}
			if ModelProbeConfiguredEgressOnly(channel.Type) && (len(request.EndpointIDs) != 1 || endpointID != 0) {
				return batch, errors.New("Account model tests must use configured egress")
			}
			batch.ChannelID = channel.Id
			endpoint, err := model.ResolveModelProbeEndpoint(ctx, userID, channel, endpointID)
			if err != nil {
				return batch, errors.New("Selected endpoint is unavailable or not permitted")
			}
			sample := model.ModelEndpointHealth{ChannelID: channel.Id, Model: name, EndpointID: endpoint.ID, SubscriptionID: endpoint.SubscriptionID, ScopeKey: model.ModelEndpointHealthScope(channel, name, endpoint.Endpoint, endpoint.ProxyURL)}
			started := time.Now()
			attemptCtx, cancel := context.WithTimeout(ctx, ModelProbeAttemptTimeout)
			outcome := execute(attemptCtx, channel, name, endpoint)
			if attemptCtx.Err() != nil {
				outcome = ClassifyModelProbeError(attemptCtx, nil)
			}
			cancel()
			sample.Status, sample.Reason, sample.HTTPStatus = outcome.Status, outcome.Reason, outcome.HTTPStatus
			sample.LatencyMS, sample.CheckedAt = time.Since(started).Milliseconds(), time.Now().Unix()
			if outcome.Reason == "cancelled" {
				stopped = "cancelled"
			} else {
				// Recheck ACL before persisting a result; revocation does not leave
				// new readable health data for an unauthorized caller.
				current, err := authorize(ctx)
				if err != nil {
					return batch, err
				}
				currentEndpoint := endpoint.Endpoint
				if endpoint.ID > 0 {
					currentEndpoint, err = model.ModelProbeEndpointVisible(ctx, userID, endpoint.ID)
					if err != nil {
						return batch, errors.New("Selected endpoint is unavailable or not permitted")
					}
				}
				if model.ModelEndpointHealthScope(current, name, currentEndpoint, endpoint.ProxyURL) != sample.ScopeKey {
					return batch, errors.New("Channel or endpoint settings changed during the test. Please try again")
				}
				stored, err := model.RecordModelEndpointHealth(ctx, sample)
				if err != nil {
					return batch, errors.New("Unable to save model test measurements")
				}
				sample = stored
			}
			batch.Results = append(batch.Results, sample)
			if sample.Status == model.ModelEndpointAvailable {
				passes[name]++
			}
			switch outcome.Reason {
			case "access_denied", "rate_limited", "quota_exhausted", "transport_error", "output_limit_exceeded":
				stopped = "not_run"
			}
		}
	}
	for _, name := range request.Models {
		if passes[name] == len(request.EndpointIDs) {
			batch.PassedModels = append(batch.PassedModels, name)
		}
	}
	return batch, nil
}

// ModelProbeConfiguredEgressOnly identifies credential-bound accounts. An
// explicit model test is not permission to move a logged-in account to a new IP.
func ModelProbeConfiguredEgressOnly(channelType int) bool {
	switch channelType {
	case constant.ChannelTypeWorkBuddy, constant.ChannelTypeTrae, constant.ChannelTypeCline, constant.ChannelTypeRaccoon, constant.ChannelTypeKimiWork, constant.ChannelTypeMiMo, constant.ChannelTypeZCode, constant.ChannelTypeQoder, constant.ChannelTypeNousPortal:
		return true
	default:
		return false
	}
}
