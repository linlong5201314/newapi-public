package model

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"gorm.io/gorm"
)

// ModelProbeEndpoint intentionally excludes names, addresses and credentials.
// Endpoint 0 means the channel's configured, stable egress, not forced direct.
type ModelProbeEndpoint struct {
	ID             int            `json:"id"`
	SubscriptionID int            `json:"subscription_id"`
	Public         bool           `json:"public"`
	Status         string         `json:"status"`
	Endpoint       *ProxyEndpoint `json:"-"`
	ProxyURL       string         `json:"-"`
}

func ModelProbeEndpointVisible(ctx context.Context, userID, endpointID int) (*ProxyEndpoint, error) {
	var endpoint ProxyEndpoint
	if err := DB.WithContext(ctx).Where("id = ? AND enabled = ? AND (user_id = ? OR public = ?)", endpointID, true, userID, true).First(&endpoint).Error; err != nil {
		return nil, err
	}
	if endpoint.SubscriptionId > 0 {
		var sub ProxySubscription
		if err := DB.WithContext(ctx).Select("id").Where("id = ? AND enabled = ? AND (user_id = ? OR public = ?)", endpoint.SubscriptionId, true, userID, true).First(&sub).Error; err != nil {
			return nil, err
		}
	}
	return &endpoint, nil
}

// modelProbeEndpointQuery applies the visibility + subscription ACL shared by
// the paginated listing and the eligible-total count. Names and addresses never
// leave this package through the model-probe management contract.
func modelProbeEndpointQuery(ctx context.Context, userID int, publicOnly bool) *gorm.DB {
	query := DB.WithContext(ctx).Model(&ProxyEndpoint{}).Where("enabled = ? AND (user_id = ? OR public = ?)", true, userID, true)
	if publicOnly {
		query = query.Where("public = ?", true)
	}
	return query.Where("subscription_id = 0 OR subscription_id IN (?)", DB.Model(&ProxySubscription{}).Select("id").Where("enabled = ? AND (user_id = ? OR public = ?)", true, userID, true))
}

func ListModelProbeEndpoints(ctx context.Context, userID, offset, limit int, publicOnly bool) ([]ModelProbeEndpoint, bool, error) {
	result := []ModelProbeEndpoint{}
	if offset < 0 || offset > 1000000 || limit < 1 || limit > 100 {
		return nil, false, fmt.Errorf("invalid endpoint pagination")
	}
	var endpoints []ProxyEndpoint
	if err := modelProbeEndpointQuery(ctx, userID, publicOnly).Select("id, subscription_id, public").Order("id ASC").Offset(offset).Limit(limit + 1).Find(&endpoints).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(endpoints) > limit
	if hasMore {
		endpoints = endpoints[:limit]
	}
	for _, endpoint := range endpoints {
		result = append(result, ModelProbeEndpoint{ID: endpoint.Id, SubscriptionID: endpoint.SubscriptionId, Public: endpoint.Public, Status: ModelEndpointUnknown})
	}
	return result, hasMore, nil
}

// CountModelProbeEndpoints returns the total eligible endpoints under the same
// ACL as ListModelProbeEndpoints, so a sampled batch can report its size
// against the full eligible population without enumerating it.
func CountModelProbeEndpoints(ctx context.Context, userID int, publicOnly bool) (int64, error) {
	var total int64
	err := modelProbeEndpointQuery(ctx, userID, publicOnly).Distinct("id").Count(&total).Error
	return total, err
}

// PeekModelProbeConfiguredEndpoint does not select, hot-load or change an exit.
// It can read fixed/direct settings and already-established stable bindings.
func PeekModelProbeConfiguredEndpoint(ctx context.Context, userID int, channel *Channel) (ModelProbeEndpoint, bool) {
	result := ModelProbeEndpoint{Status: ModelEndpointUnknown}
	settings := channel.GetSetting()
	policy := settings.ProxyPolicy
	if policy == nil {
		if settings.Proxy == "direct" || settings.Proxy == "" && channel.OwnerId == 0 {
			return result, true
		}
		if settings.Proxy != "" && settings.Proxy != "userpool" {
			result.ProxyURL = settings.Proxy
			return result, true
		}
	} else {
		if policy.Mode == "direct" {
			return result, true
		}
		if policy.Mode == "fixed" {
			result.ProxyURL = settings.Proxy
			return result, true
		}
		if policy.Mode == "endpoint" {
			endpoint, err := ModelProbeEndpointVisible(ctx, userID, policy.EndpointID)
			if err != nil {
				return result, false
			}
			result.ID, result.SubscriptionID, result.Public, result.Endpoint = endpoint.Id, endpoint.SubscriptionId, endpoint.Public, endpoint
			return result, true
		}
	}
	legacyScope := policy == nil
	if policy == nil {
		policy = &dto.ChannelProxyPolicy{Mode: "user_pool"}
	}
	identity := proxyAccountIdentity(fmt.Sprintf("channel:%d", channel.Id))
	alias, err := proxyStickyValue(fmt.Sprintf("proxy-pool:sticky:v1:owner:%d:channel:%d", channel.OwnerId, channel.Id), nil, "")
	if err != nil {
		return result, false
	}
	if alias != "" {
		identity = alias
	}
	key := fmt.Sprintf("proxy-pool:sticky:v1:owner:%d:account:%s:scope:%s:%d:%d:%t", channel.OwnerId, identity, policy.Mode, policy.EndpointID, policy.SubscriptionID, legacyScope)
	bound, err := proxyStickyValue(key, nil, "")
	if err != nil || bound == "" {
		return result, false
	}
	endpointID, err := strconv.Atoi(bound)
	if err != nil || endpointID <= 0 {
		return result, false
	}
	endpoint, err := ModelProbeEndpointVisible(ctx, userID, endpointID)
	if err != nil {
		return result, false
	}
	result.ID, result.SubscriptionID, result.Public, result.Endpoint = endpoint.Id, endpoint.SubscriptionId, endpoint.Public, endpoint
	return result, true
}

// ResolveModelProbeEndpoint never changes an endpoint, subscription, channel or
// an account's sticky scope. Explicit endpoint probes use a fixed authorized
// listener; default probes use the same stable resolver as production inference.
func ResolveModelProbeEndpoint(ctx context.Context, userID int, channel *Channel, endpointID int) (ModelProbeEndpoint, error) {
	result := ModelProbeEndpoint{ID: endpointID, Status: ModelEndpointUnknown}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if endpointID == 0 {
		proxyURL, err := ResolveStableChannelProxy(channel.OwnerId, channel.Id, "legacy", channel.GetSetting())
		if err != nil {
			return result, err
		}
		result.ProxyURL = proxyURL
		if value, ok := stableProxyEndpoints.Load(sha256.Sum256([]byte(proxyURL))); proxyURL != "" && ok {
			identity := value.(stableProxyEndpoint)
			endpoint, err := ModelProbeEndpointVisible(ctx, userID, identity.id)
			if err != nil || sha256.Sum256([]byte(endpoint.Url+"\x00"+endpoint.RawUri)) != identity.fingerprint {
				return result, fmt.Errorf("model probe endpoint unavailable")
			}
			result.ID, result.SubscriptionID, result.Public, result.Endpoint = endpoint.Id, endpoint.SubscriptionId, endpoint.Public, endpoint
		}
		return result, nil
	}
	endpoint, err := ModelProbeEndpointVisible(ctx, userID, endpointID)
	if err != nil {
		return result, err
	}
	// Root platform tests may use only public exits, never the root's private
	// personal nodes. A public endpoint's subscription ACL was checked above.
	if channel.OwnerId == 0 && !endpoint.Public {
		return result, fmt.Errorf("model probe endpoint unavailable")
	}
	proxyURL := strings.TrimSpace(endpoint.Url)
	if proxyURL == "" {
		proxyURL, err = selectedEndpointProxyURLProvider(*endpoint)
		if err != nil {
			return result, err
		}
	}
	if parsed, err := common.ParseProxyURLStrict(proxyURL); err != nil || parsed == nil {
		return result, fmt.Errorf("model probe endpoint unavailable")
	}
	result.ProxyURL, result.Endpoint, result.SubscriptionID, result.Public = proxyURL, endpoint, endpoint.SubscriptionId, endpoint.Public
	return result, nil
}
