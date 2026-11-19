package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// Protocol: HaizhuAI/Zkali@0770d34, cli-oauth.ts. No login JWT refresh
// contract is implemented by the upstream adapters; expiry requires sign-in.
const ZCodeAPIBase = "https://zcode.z.ai/api/v1"
const ZCodeStartBase = ZCodeAPIBase + "/zcode-plan/anthropic"
const ZCodeAppVersion = "3.17.2"

type ZCodeTicket struct {
	FlowID    string `json:"flowId"`
	PollToken string `json:"pollToken"`
	Until     int64  `json:"until"`
	Interval  int    `json:"interval"`
	Provider  string `json:"provider"`
}

// Only the Start Plan JWT is retained, not the unrelated business or refresh
// tokens included in the official consent response. Stored encrypted by caller.
type ZCodeCredential struct {
	Provider  string        `json:"provider"`
	UserID    string        `json:"userId"`
	Token     string        `json:"token"`
	ExpiresAt int64         `json:"expiresAt"`
	Egress    AccountEgress `json:"egress"`
}

func ValidZCodeAuthorizationURL(target string) bool {
	if len(target) > 8192 {
		return false
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return false
	}
	switch u.Host {
	case "bigmodel.cn":
		return u.Path == "/login"
	case "chat.z.ai":
		return u.Path == "/api/oauth/authorize"
	case "zcode.z.ai":
		return u.Path == "/app/oauth/login"
	}
	return false
}

func zcodeProvider(provider string) bool { return provider == "bigmodel" || provider == "zai" }

func zcodeLoginRequest(ctx context.Context, proxy, method, path, token string, body []byte) ([]byte, error) {
	client, err := GetAccountAuthHTTPClient(proxy, "")
	if err != nil || client == nil {
		return nil, ErrAccountEgress
	}
	copyClient := *client
	copyClient.Timeout = 8 * time.Second
	copyClient.Jar = nil
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, method, ZCodeAPIBase+path, bytes.NewReader(body))
	if err != nil {
		return nil, ErrAccountProtocol
	}
	req.GetBody = nil // Never replay credential-bearing authorization requests.
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := copyClient.Do(req)
	if err != nil {
		return nil, ErrAccountUpstream
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, ErrDesktopLoginExpired
	}
	if res.StatusCode != http.StatusOK {
		return nil, ErrAccountUpstream
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, ErrAccountProtocol
	}
	var envelope struct {
		Code *int              `json:"code"`
		Data common.RawMessage `json:"data"`
	}
	if common.Unmarshal(raw, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 || len(envelope.Data) == 0 {
		return nil, ErrAccountProtocol
	}
	return envelope.Data, nil
}

func StartZCodeSignIn(ctx context.Context, proxy, provider string) (*ZCodeTicket, string, error) {
	if !zcodeProvider(provider) {
		return nil, "", ErrAccountProtocol
	}
	opaque := make([]byte, 32)
	if _, err := rand.Read(opaque); err != nil {
		return nil, "", ErrAccountProtocol
	}
	pollToken := hex.EncodeToString(opaque)
	body, err := common.Marshal(map[string]string{"provider": provider})
	if err != nil {
		return nil, "", ErrAccountProtocol
	}
	raw, err := zcodeLoginRequest(ctx, proxy, http.MethodPost, "/oauth/cli/init", pollToken, body)
	if err != nil {
		return nil, "", err
	}
	var result struct {
		FlowID   string `json:"flow_id"`
		AuthURL  string `json:"authorize_url"`
		Until    int64  `json:"expires_at"`
		Interval int    `json:"poll_interval_sec"`
	}
	if common.Unmarshal(raw, &result) != nil || !HeaderCredential(result.FlowID, 256) || strings.ContainsAny(result.FlowID, "/\\?#%") || !ValidZCodeAuthorizationURL(result.AuthURL) || result.Until <= time.Now().Unix() || result.Interval < 1 || result.Interval > 60 {
		return nil, "", ErrAccountProtocol
	}
	ticket := &ZCodeTicket{FlowID: result.FlowID, PollToken: pollToken, Until: min(result.Until, time.Now().Add(10*time.Minute).Unix()), Interval: result.Interval, Provider: provider}
	return ticket, result.AuthURL, nil
}

func PollZCodeSignIn(ctx context.Context, proxy string, ticket *ZCodeTicket) (*ZCodeCredential, error) {
	if ticket == nil || !zcodeProvider(ticket.Provider) || !HeaderCredential(ticket.FlowID, 256) || strings.ContainsAny(ticket.FlowID, "/\\?#%") || len(ticket.PollToken) != 64 {
		return nil, ErrAccountProtocol
	}
	if _, err := hex.DecodeString(ticket.PollToken); err != nil {
		return nil, ErrAccountProtocol
	}
	if ticket.Until <= time.Now().Unix() {
		return nil, model.ErrAuthFlowExpired
	}
	raw, err := zcodeLoginRequest(ctx, proxy, http.MethodGet, "/oauth/cli/poll/"+url.PathEscape(ticket.FlowID), ticket.PollToken, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Status string `json:"status"`
		Token  string `json:"token"`
		User   struct {
			ID string `json:"user_id"`
		} `json:"user"`
		BigModel struct {
			Access string `json:"access_token"`
			Camel  string `json:"accessToken"`
		} `json:"bigmodel"`
		ZAI struct {
			Access string `json:"access_token"`
			Camel  string `json:"accessToken"`
		} `json:"zai"`
	}
	if common.Unmarshal(raw, &result) != nil {
		return nil, ErrAccountProtocol
	}
	if result.Status == "pending" {
		return nil, nil
	}
	access := result.BigModel.Access
	if access == "" {
		access = result.BigModel.Camel
	}
	if ticket.Provider == "zai" {
		access = result.ZAI.Access
		if access == "" {
			access = result.ZAI.Camel
		}
	}
	if result.Status != "ready" || !HeaderCredential(result.Token, 16384) || !HeaderCredential(result.User.ID, 256) || !HeaderCredential(access, 16384) {
		return nil, ErrAccountProtocol
	}
	until, err := zcodeJWTExpiry(result.Token)
	if err != nil {
		return nil, err
	}
	return &ZCodeCredential{Provider: ticket.Provider, UserID: result.User.ID, Token: result.Token, ExpiresAt: until}, nil
}

// JWT decoding supplies only expiry, never identity or authenticity. The JWT
// comes from authenticated one-use provider polling and is validated by balance.
func zcodeJWTExpiry(token string) (int64, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, ErrAccountProtocol
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, ErrAccountProtocol
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if common.Unmarshal(raw, &claims) != nil || claims.Exp <= time.Now().Unix() {
		return 0, ErrDesktopLoginExpired
	}
	return claims.Exp, nil
}
