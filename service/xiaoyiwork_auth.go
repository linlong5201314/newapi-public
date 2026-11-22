package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

const (
	XiaoyiWorkOAuthClientID   = "6917612679526496902"
	XiaoyiWorkCallbackPort    = 17355
	XiaoyiWorkCallbackPath    = "/auth/callback"
	XiaoyiWorkOAuthAuthorize  = "https://oauth-login.cloud.huawei.com/oauth2/v3/authorize"
	xiaoyiWorkOAuthToken      = "https://oauth-login.cloud.huawei.com/oauth2/v3/token"
	xiaoyiWorkHAG             = "https://hag-drcn.op.dbankcloud.com"
	XiaoyiWorkSSEEndpoint     = xiaoyiWorkHAG + "/celia/v1/sse-api/chat/completions"
	xiaoyiWorkGallery         = xiaoyiWorkHAG + "/user-ability-gallery/v1/agent/user/query"
	xiaoyiWorkCredentialIssue = xiaoyiWorkHAG + "/user-credential/v1/business-credential/create"
	xiaoyiWorkRefresh         = xiaoyiWorkHAG + "/user-credential/v1/xiaoyiwork/refresh-token"
	xiaoyiWorkAgentID         = "agent208b6760552b4fa0b395e5165d0d771e"
)

var errXiaoyiWorkUpstream = errors.New("Xiaoyi Work account authorization failed. Sign in again or retry later")

// XiaoyiWorkProviderError contains only the protocol stage and HTTP status.
// Response bodies and upstream messages are never logged or returned because
// providers can include account details in otherwise ordinary error payloads.
type XiaoyiWorkProviderError struct {
	Stage  string
	Status int
	Detail string
}

func (err *XiaoyiWorkProviderError) Error() string {
	if err.Status > 0 {
		return fmt.Sprintf("Xiaoyi Work %s was rejected (HTTP %d)", err.Stage, err.Status)
	}
	if err.Detail != "" {
		return fmt.Sprintf("Xiaoyi Work %s failed: %s", err.Stage, err.Detail)
	}
	return "Xiaoyi Work " + err.Stage + " failed"
}

func xiaoyiWorkProtocolError(stage string, status int) error {
	return &XiaoyiWorkProviderError{Stage: stage, Status: status}
}

var xiaoyiWorkTransport = func() http.RoundTripper { return nil }

// SetXiaoyiWorkTransportForTest only replaces the outbound HTTP transport; it
// never changes provider URLs, session validation or the account model catalog.
func SetXiaoyiWorkTransportForTest(transport http.RoundTripper) (restore func()) {
	previous := xiaoyiWorkTransport
	xiaoyiWorkTransport = func() http.RoundTripper { return transport }
	return func() { xiaoyiWorkTransport = previous }
}

type XiaoyiWorkOAuthSession struct {
	Verifier     string `json:"verifier"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	UID          string `json:"uid"`
	DeviceID     string `json:"device_id"`
	Credential   string `json:"credential"`
	ValidTimeSec int64  `json:"valid_time_sec"`
}

type XiaoyiWorkCatalogEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Rate string `json:"rate,omitempty"`
	Gray bool   `json:"gray"`
}

func XiaoyiWorkRandom(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func XiaoyiWorkAuthorizeURL(state, verifier string) (string, error) {
	digest := sha256.Sum256([]byte(verifier))
	values := url.Values{
		"response_type": {"code"}, "access_type": {"offline"}, "client_id": {XiaoyiWorkOAuthClientID},
		"redirect_uri":   {fmt.Sprintf("http://127.0.0.1:%d%s", XiaoyiWorkCallbackPort, XiaoyiWorkCallbackPath)},
		"scope":          {"openid profile https://www.huawei.com/auth/drive https://www.huawei.com/auth/drive.xiaoyiwork"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"},
		"state": {state}, "display": {"page"},
	}
	return XiaoyiWorkOAuthAuthorize + "?" + values.Encode(), nil
}

func XiaoyiWorkMachineID() string {
	digest := sha256.Sum256([]byte("new-api:xiaoyiwork:" + common.SessionSecret))
	return fmt.Sprintf("%x", digest[:])
}

func xiaoyiWorkRequest(ctx context.Context, client *http.Client, method, endpoint string, body any, headers http.Header, out any) error {
	stage := "provider request"
	switch endpoint {
	case xiaoyiWorkOAuthToken:
		stage = "OAuth token exchange"
	case xiaoyiWorkCredentialIssue:
		stage = "device credential issuance"
	case xiaoyiWorkGallery:
		stage = "model catalog request"
	case xiaoyiWorkRefresh:
		stage = "credential refresh"
	}
	if client == nil {
		return errXiaoyiWorkUpstream
	}
	var reader io.Reader
	if body != nil {
		switch value := body.(type) {
		case url.Values:
			reader = strings.NewReader(value.Encode())
		default:
			raw, err := common.Marshal(body)
			if err != nil {
				return err
			}
			reader = bytes.NewReader(raw)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return errXiaoyiWorkUpstream
	}
	for key, values := range headers {
		request.Header.Del(key)
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if body != nil && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return &XiaoyiWorkProviderError{Stage: stage, Detail: "upstream connection failed"}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
	if err != nil || len(raw) > 2<<20 {
		return errXiaoyiWorkUpstream
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return xiaoyiWorkProtocolError(stage, response.StatusCode)
	}
	if out != nil {
		if err := common.Unmarshal(raw, out); err != nil {
			return &XiaoyiWorkProviderError{Stage: stage, Detail: "invalid provider response"}
		}
	}
	return nil
}

func xiaoyiWorkHTTPClient(proxy string) (*http.Client, error) {
	client, err := GetAccountAuthHTTPClient(proxy, "")
	if err != nil {
		return nil, err
	}
	clone := *client
	if transport := xiaoyiWorkTransport(); transport != nil {
		clone.Transport = transport
	}
	clone.Timeout = 30 * time.Second
	return &clone, nil
}

func XiaoyiWorkExchange(ctx context.Context, proxy, code, verifier string) (*XiaoyiWorkOAuthSession, error) {
	client, err := xiaoyiWorkHTTPClient(proxy)
	if err != nil {
		return nil, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {XiaoyiWorkOAuthClientID},
		"code_verifier": {verifier}, "redirect_uri": {fmt.Sprintf("http://127.0.0.1:%d%s", XiaoyiWorkCallbackPort, XiaoyiWorkCallbackPath)}}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	if err := xiaoyiWorkRequest(ctx, client, http.MethodPost, xiaoyiWorkOAuthToken, form, http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, &payload); err != nil {
		return nil, err
	}
	parts := strings.Split(payload.IDToken, ".")
	if len(parts) != 3 {
		return nil, errXiaoyiWorkUpstream
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errXiaoyiWorkUpstream
	}
	var claims struct {
		UID string `json:"uid"`
	}
	if common.Unmarshal(claimsRaw, &claims) != nil || strings.TrimSpace(claims.UID) == "" || payload.AccessToken == "" || payload.RefreshToken == "" {
		return nil, &XiaoyiWorkProviderError{Stage: "OAuth identity validation", Detail: "missing account identity"}
	}
	session := &XiaoyiWorkOAuthSession{Verifier: verifier, AccessToken: payload.AccessToken, RefreshToken: payload.RefreshToken, UID: claims.UID, DeviceID: XiaoyiWorkMachineID() + "_" + claims.UID}
	if err := session.IssueCredential(ctx, client); err != nil {
		return nil, err
	}
	return session, nil
}

func (session *XiaoyiWorkOAuthSession) IssueCredential(ctx context.Context, client *http.Client) error {
	var response struct {
		CredentialResult struct {
			BusinessCredential string `json:"businessCredential"`
			Credential         string `json:"credential"`
			ValidTime          int64  `json:"validTime"`
		} `json:"credentialResult"`
		Data struct {
			BusinessCredential string `json:"businessCredential"`
			Credential         string `json:"credential"`
			ValidTime          int64  `json:"validTime"`
		} `json:"data"`
		BusinessCredential string `json:"businessCredential"`
		Credential         string `json:"credential"`
		ValidTime          int64  `json:"validTime"`
	}
	if err := xiaoyiWorkRequest(ctx, client, http.MethodPost, xiaoyiWorkCredentialIssue, map[string]any{
		"deviceId": session.DeviceID, "uid": session.UID, "clientId": XiaoyiWorkOAuthClientID, "sysVer": "5.0.0", "version": "1.0.0", "ts": strconv.FormatInt(time.Now().UnixMilli(), 10),
	}, http.Header{"Authorization": {"Bearer " + session.AccessToken}, "x-uid": {session.UID}, "x-client-version": {"1.0.0"}, "x-hag-trace-id": {newTraceID()}}, &response); err != nil {
		return err
	}
	session.Credential = firstNonEmpty(response.CredentialResult.BusinessCredential, response.CredentialResult.Credential, response.Data.BusinessCredential, response.Data.Credential, response.BusinessCredential, response.Credential)
	if session.Credential == "" {
		return &XiaoyiWorkProviderError{Stage: "device credential issuance", Detail: "provider returned no credential"}
	}
	session.ValidTimeSec = firstPositive(response.CredentialResult.ValidTime, response.Data.ValidTime, response.ValidTime)
	if session.ValidTimeSec == 0 {
		session.ValidTimeSec = time.Now().Add(7 * 24 * time.Hour).Unix()
	}
	return nil
}

func (session *XiaoyiWorkOAuthSession) Headers(trace string) http.Header {
	return http.Header{"Content-Type": {"application/json"}, "businessCredential": {session.Credential}, "x-uid": {session.UID}, "x-device-id": {session.DeviceID}, "x-request-from": {"xiaoyiWork"}, "x-businessId": {"WINPC_XIAOYI_WORK"}, "x-hag-trace-id": {trace}}
}

func (session *XiaoyiWorkOAuthSession) Refresh(ctx context.Context, client *http.Client) error {
	var response struct {
		Token string `json:"token"`
	}
	if err := xiaoyiWorkRequest(ctx, client, http.MethodPost, xiaoyiWorkRefresh, map[string]any{"refreshToken": session.RefreshToken}, session.Headers(newTraceID()), &response); err != nil {
		return err
	}
	if response.Token == "" {
		return errXiaoyiWorkUpstream
	}
	session.AccessToken = response.Token
	return session.IssueCredential(ctx, client)
}

func (session *XiaoyiWorkOAuthSession) Fresh(ctx context.Context, proxy string) error {
	if session.ValidTimeSec > time.Now().Add(12*time.Hour).Unix() {
		return nil
	}
	client, err := xiaoyiWorkHTTPClient(proxy)
	if err != nil {
		return err
	}
	return session.Refresh(ctx, client)
}

func FetchXiaoyiWorkCatalog(ctx context.Context, proxy string, session *XiaoyiWorkOAuthSession) ([]XiaoyiWorkCatalogEntry, error) {
	if session == nil || session.Credential == "" {
		return nil, errXiaoyiWorkUpstream
	}
	client, err := xiaoyiWorkHTTPClient(proxy)
	if err != nil {
		return nil, err
	}
	var response struct {
		Contents []struct {
			ModelList []struct {
				ID   string `json:"modelValue"`
				Name string `json:"modelName"`
				Rate string `json:"aiPointConsumptionRatio"`
				Gray bool   `json:"gray"`
			} `json:"modelList"`
		} `json:"contents"`
	}
	headers := session.Headers(newTraceID())
	headers.Set("Accept", "application/json")
	headers.Set("x-prd-pkg-name", "com.huawei.hmos.vassistant")
	if err := xiaoyiWorkRequest(ctx, client, http.MethodPost, xiaoyiWorkGallery, map[string]any{
		"type": "CELIA_CLAW_USER_MODEL_QUERY", "terminalInfo": map[string]any{
			"agentId": xiaoyiWorkAgentID, "brand": "HUAWEI", "countryCode": "CN", "deviceType": "PC", "language": "zh_CN", "locale": "zh-CN", "manufacturer": "HUAWEI", "osType": "Hap", "phoneType": "ALN-AL80", "prdVer": "11.6.5.414", "privacyOption": map[string]any{}, "sysVer": "OpenHarmony-6.1.1.35(Beta1)", "ohosApiVersion": 24,
		}, "pagination": map[string]any{"limit": 500, "start": "0"},
	}, headers, &response); err != nil {
		return nil, err
	}
	catalog := make([]XiaoyiWorkCatalogEntry, 0)
	seen := make(map[string]bool)
	for _, content := range response.Contents {
		for _, item := range content.ModelList {
			id := strings.TrimSpace(item.ID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = id
			}
			catalog = append(catalog, XiaoyiWorkCatalogEntry{ID: id, Name: name, Rate: item.Rate, Gray: item.Gray})
		}
	}
	return catalog, nil
}

type xiaoyiWorkToolArguments string

func (arguments *xiaoyiWorkToolArguments) UnmarshalJSON(data []byte) error {
	var text string
	if err := common.Unmarshal(data, &text); err == nil {
		*arguments = xiaoyiWorkToolArguments(text)
		return nil
	}
	var value any
	if err := common.Unmarshal(data, &value); err != nil {
		return err
	}
	raw, err := common.Marshal(value)
	if err != nil {
		return err
	}
	*arguments = xiaoyiWorkToolArguments(string(raw))
	return nil
}

type xiaoyiWorkFrame struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			Reasoning string `json:"reasoning_content"`
			ToolCalls []struct {
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function *struct {
					Name      string                  `json:"name"`
					Arguments xiaoyiWorkToolArguments `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *dto.Usage `json:"usage"`
}

func XiaoyiWorkNormalizeStream(upstream io.Reader, downstream io.Writer, model string) error {
	reader := bufio.NewScanner(upstream)
	reader.Buffer(make([]byte, 64<<10), 4<<20)
	for reader.Scan() {
		block := reader.Text()
		data := sseData(block)
		if data == "" || data == "[DONE]" {
			continue
		}
		var frame xiaoyiWorkFrame
		if common.Unmarshal([]byte(data), &frame) != nil {
			continue
		}
		if frame.Object == "" {
			frame.Object = "chat.completion.chunk"
		}
		if frame.Created == 0 {
			frame.Created = time.Now().Unix()
		}
		if frame.Model == "" {
			frame.Model = model
		}
		if frame.ID == "" {
			frame.ID = "chatcmpl-" + newTraceID()
		}
		for index := range frame.Choices {
			frame.Choices[index].Index = index
		}
		raw, err := common.Marshal(frame)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(downstream, "data: %s\n\n", raw); err != nil {
			return err
		}
	}
	if err := reader.Err(); err != nil {
		return err
	}
	_, err := io.WriteString(downstream, "data: [DONE]\n\n")
	return err
}

func XiaoyiWorkAggregate(upstream io.Reader, model string) (*dto.OpenAITextResponse, error) {
	response := &dto.OpenAITextResponse{Id: "chatcmpl-" + newTraceID(), Object: "chat.completion", Created: time.Now().Unix(), Model: model}
	message := dto.Message{Role: "assistant"}
	var finish string
	usage := dto.Usage{}
	tools := make(map[int]*dto.ToolCallResponse)
	err := XiaoyiWorkNormalizeStream(upstream, &streamCollector{write: func(data []byte) error {
		var frame xiaoyiWorkFrame
		if common.Unmarshal(data, &frame) != nil {
			return nil
		}
		if frame.Usage != nil {
			usage = *frame.Usage
		}
		for _, choice := range frame.Choices {
			message.Content = message.StringContent() + choice.Delta.Content
			message.ReasoningContent = common.GetPointer(message.GetReasoningContent() + choice.Delta.Reasoning)
			if choice.FinishReason != nil {
				finish = *choice.FinishReason
			}
			for index, call := range choice.Delta.ToolCalls {
				key := index
				if call.Index != nil {
					key = *call.Index
				}
				entry := tools[key]
				if entry == nil {
					entry = &dto.ToolCallResponse{Type: "function"}
					tools[key] = entry
				}
				if call.ID != "" {
					entry.ID = call.ID
				}
				if call.Function != nil {
					if call.Function.Name != "" {
						entry.Function.Name += call.Function.Name
					}
					entry.Function.Arguments += string(call.Function.Arguments)
				}
			}
		}
		return nil
	}}, model)
	if err != nil {
		return nil, err
	}
	if len(tools) > 0 {
		toolCalls := make([]dto.ToolCallResponse, 0, len(tools))
		for index := 0; index < len(tools); index++ {
			toolCalls = append(toolCalls, *tools[index])
		}
		message.ToolCalls, err = common.Marshal(toolCalls)
		if err != nil {
			return nil, err
		}
		if finish == "" {
			finish = "tool_calls"
		}
	}
	if finish == "" {
		finish = "stop"
	}
	response.Choices = []dto.OpenAITextResponseChoice{{Index: 0, Message: message, FinishReason: finish}}
	response.Usage = usage
	return response, nil
}

type streamCollector struct{ write func([]byte) error }

func (c *streamCollector) Write(data []byte) (int, error) {
	for _, part := range bytes.Split(data, []byte("\n\n")) {
		if len(bytes.TrimSpace(part)) == 0 {
			continue
		}
		if err := c.write(bytes.TrimSpace(bytes.TrimPrefix(part, []byte("data: ")))); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func sseData(block string) string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(block, "\r", ""), "\n") {
		if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return strings.Join(lines, "\n")
}

func newTraceID() string {
	value, err := XiaoyiWorkRandom(8)
	if err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "mp-" + value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
