package service

// Protocol reference: magpie-community/plugins@e6c457112ababf96d9415fe0a217ab0b7429ae98,
// packages/cline/index.mjs and README.md, verified 2026-11-19.
// Protocol adaptation from the MIT-licensed source:
//
// Copyright (c) 2026 magpie-community contributors
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const ClineAPIBase = "https://api.cline.bot/api/v1"

const (
	clineWorkOSClient        = "client_01K3A541FN8TA3EPPHTD2325AR"
	clineDeviceEndpoint      = "https://api.workos.com/user_management/authorize/device"
	clineTokenEndpoint       = "https://api.workos.com/user_management/authenticate"
	clineDeviceGrant         = "urn:ietf:params:oauth:grant-type:device_code"
	clineRecommendedEndpoint = ClineAPIBase + "/ai/cline/recommended-models"
	clineModelsEndpoint      = ClineAPIBase + "/ai/cline/models"
)

// ClineTicket is a bearer secret. The broker stores it encrypted, binds it to
// its owner/egress, enforces Interval, and consumes the flow exactly once.
// Poll changes only Interval on slow_down; persist that change before retrying.
type ClineTicket struct {
	DeviceCode string `json:"deviceCode"`
	Until      int64  `json:"until"`
	Interval   int    `json:"interval"`
}

// Token is the raw Cline access token, without the gateway's workos: prefix.
// The caller encrypts the entire value and commits rotated token pairs together.
type ClineCredential struct {
	Token        string        `json:"token"`
	UserID       string        `json:"userId"`
	ExpiresAt    int64         `json:"expiresAt"`
	RefreshToken string        `json:"refreshToken,omitempty"`
	Egress       AccountEgress `json:"egress"`
}

// ClineHeaders returns a fresh copy of the client identity from the pinned
// protocol source. It never carries credentials or a caller's forwarded headers.
func ClineHeaders() http.Header {
	return http.Header{
		"Http-Referer":       {"https://cline.bot"},
		"X-Title":            {"Cline"},
		"User-Agent":         {"Cline/3.0.68"},
		"X-Is-Multiroot":     {"false"},
		"X-Client-Type":      {"cline-cli"},
		"X-Client-Version":   {"3.0.68"},
		"X-Platform":         {"cli"},
		"X-Platform-Version": {"3.0.68"},
		"X-Core-Version":     {"0.0.90"},
	}
}

// Only these fixed protocol endpoints are callable. No provider-returned URL
// is requested; browser URLs are independently validated and returned only.
func clineRequest(ctx context.Context, proxy, endpoint string, body []byte) (int, []byte, error) {
	if ctx == nil {
		return 0, nil, ErrAccountProtocol
	}
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	method, contentType := http.MethodPost, "application/json"
	timeout, limit := 15*time.Second, int64(65536)
	switch endpoint {
	case clineDeviceEndpoint, clineTokenEndpoint:
		contentType = "application/x-www-form-urlencoded"
	case ClineAPIBase + "/auth/register":
		timeout = 30 * time.Second
	case ClineAPIBase + "/auth/refresh":
	case clineRecommendedEndpoint, clineModelsEndpoint:
		method, contentType, limit = http.MethodGet, "", 1<<20
	default:
		return 0, nil, ErrAccountProtocol
	}
	client, err := GetAccountAuthHTTPClient(proxy, "")
	if err != nil || client == nil {
		return 0, nil, ErrAccountEgress
	}
	copyClient := *client
	copyClient.Timeout = timeout
	copyClient.Jar = nil
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, ErrAccountProtocol
	}
	req.GetBody = nil // Authorization codes and rotated refresh tokens are never replayed.
	if strings.HasPrefix(endpoint, ClineAPIBase+"/") {
		req.Header = ClineHeaders()
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := copyClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, ErrAccountUpstream
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, ErrAccountUpstream
	}
	if int64(len(raw)) > limit {
		return 0, nil, ErrAccountProtocol
	}
	return res.StatusCode, raw, nil
}

// ValidClineAuthorizationURL accepts only the exact WorkOS tenant browser
// destination observed from public device initialization on 2026-11-19.
// Unknown query keys (especially redirect/token parameters) are not forwarded.
func ValidClineAuthorizationURL(target string) bool {
	if len(target) > 2048 {
		return false
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Host != "authkit.cline.bot" || u.Path != "/device" || u.User != nil || u.Port() != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) > 1 {
		return false
	}
	if len(q) == 0 {
		return true
	}
	codes := q["user_code"]
	if len(codes) != 1 || len(codes[0]) == 0 || len(codes[0]) > 128 {
		return false
	}
	return strings.Trim(codes[0], "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-") == ""
}

func StartClineSignIn(ctx context.Context, proxy string) (*ClineTicket, string, error) {
	body := url.Values{"client_id": {clineWorkOSClient}}.Encode()
	status, raw, err := clineRequest(ctx, proxy, clineDeviceEndpoint, []byte(body))
	if err != nil {
		return nil, "", err
	}
	if status != http.StatusOK {
		return nil, "", ErrAccountUpstream
	}
	var answer struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		CompleteURI     string `json:"verification_uri_complete"`
		ExpiresIn       int64  `json:"expires_in"`
		Interval        *int   `json:"interval"`
	}
	if common.Unmarshal(raw, &answer) != nil || !HeaderCredential(answer.DeviceCode, 4096) || answer.ExpiresIn <= 0 || !ValidClineAuthorizationURL(answer.VerificationURI) {
		return nil, "", ErrAccountProtocol
	}
	base, _ := url.Parse(answer.VerificationURI)
	if base.RawQuery != "" || base.ForceQuery {
		return nil, "", ErrAccountProtocol
	}
	interval := 5
	if answer.Interval != nil {
		interval = *answer.Interval
	}
	if interval < 1 || interval > 60 {
		return nil, "", ErrAccountProtocol
	}
	// Construct the fallback approval URL locally rather than returning a bare
	// page which would require the user to obtain a code from another helper.
	if !ValidClineAuthorizationURL(answer.VerificationURI + "?" + url.Values{"user_code": {answer.UserCode}}.Encode()) {
		return nil, "", ErrAccountProtocol
	}
	target := answer.CompleteURI
	if target == "" {
		target = answer.VerificationURI + "?" + url.Values{"user_code": {answer.UserCode}}.Encode()
	}
	if !ValidClineAuthorizationURL(target) {
		return nil, "", ErrAccountProtocol
	}
	u, _ := url.Parse(target)
	if u.Query().Get("user_code") != answer.UserCode || answer.UserCode == answer.DeviceCode {
		return nil, "", ErrAccountProtocol
	}
	return &ClineTicket{DeviceCode: answer.DeviceCode, Until: time.Now().Unix() + min(answer.ExpiresIn, 600), Interval: interval}, target, nil
}

// PollClineSignIn performs a single poll, never sleeps and never retries a
// side-effecting request. nil,nil means pending. The broker enforces cooldowns.
// After a successful WorkOS exchange, any registration failure requires a fresh
// flow: the one-use code may already be consumed. No partial tokens are returned.
func PollClineSignIn(ctx context.Context, proxy string, ticket *ClineTicket) (*ClineCredential, error) {
	if ticket == nil || !HeaderCredential(ticket.DeviceCode, 4096) || ticket.Interval < 1 || ticket.Interval > 600 {
		return nil, ErrAccountProtocol
	}
	if ticket.Until <= time.Now().Unix() {
		return nil, model.ErrAuthFlowExpired
	}
	if ticket.Until > time.Now().Unix()+600 {
		return nil, ErrAccountProtocol
	}
	body := url.Values{"client_id": {clineWorkOSClient}, "device_code": {ticket.DeviceCode}, "grant_type": {clineDeviceGrant}}.Encode()
	status, raw, err := clineRequest(ctx, proxy, clineTokenEndpoint, []byte(body))
	if err != nil {
		return nil, err
	}
	if status >= 500 || status == http.StatusTooManyRequests {
		if status == http.StatusTooManyRequests {
			ticket.Interval = min(ticket.Interval+5, 600)
		}
		return nil, ErrAccountUpstream
	}
	if status >= 300 && status < 400 {
		return nil, ErrAccountUpstream
	}
	var answer struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Error   string `json:"error"`
	}
	if common.Unmarshal(raw, &answer) != nil {
		return nil, ErrAccountProtocol
	}
	if answer.Error != "" {
		if status != http.StatusBadRequest {
			return nil, ErrAccountProtocol
		}
		switch answer.Error {
		case "authorization_pending":
			return nil, nil
		case "slow_down":
			// RFC 8628 requires adding at least five seconds for all later polls.
			ticket.Interval = min(ticket.Interval+5, 600)
			return nil, nil
		case "expired_token":
			return nil, model.ErrAuthFlowExpired
		case "access_denied", "invalid_grant":
			return nil, ErrDesktopLoginExpired
		default:
			return nil, ErrAccountProtocol
		}
	}
	if status != http.StatusOK || !HeaderCredential(answer.Access, 16384) || (answer.Refresh != "" && !HeaderCredential(answer.Refresh, 16384)) {
		return nil, ErrAccountProtocol
	}
	if ticket.Until <= time.Now().Unix() {
		return nil, model.ErrAuthFlowExpired
	}
	registration, err := common.Marshal(map[string]string{"accessToken": answer.Access, "refreshToken": answer.Refresh})
	if err != nil {
		return nil, ErrAccountProtocol
	}
	status, raw, err = clineRequest(ctx, proxy, ClineAPIBase+"/auth/register", registration)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, ErrDesktopLoginExpired
	}
	if status != http.StatusOK {
		return nil, ErrAccountUpstream
	}
	return clineParseCredential(raw, answer.Refresh, "")
}

// clineParseCredential accepts only provider-asserted identity from the fixed
// TLS register/refresh endpoints. JWT decoding is not used as an identity proof.
// Unlike the reference client's UI fallback, missing expiry is not invented.
func clineParseCredential(raw []byte, fallbackRefresh, fallbackUser string) (*ClineCredential, error) {
	payload, err := clinePayload(raw)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Access   string            `json:"accessToken"`
		Refresh  string            `json:"refreshToken"`
		Expires  common.RawMessage `json:"expiresAt"`
		UserInfo struct {
			ClineUserID string `json:"clineUserId"`
			Subject     string `json:"subject"`
		} `json:"userInfo"`
	}
	if common.Unmarshal(payload, &answer) != nil || !HeaderCredential(answer.Access, 16384) {
		return nil, ErrAccountProtocol
	}
	user := answer.UserInfo.ClineUserID
	if user == "" {
		user = answer.UserInfo.Subject
	}
	if user == "" {
		user = fallbackUser
	}
	if !HeaderCredential(user, 256) || (fallbackUser != "" && user != fallbackUser) {
		return nil, ErrAccountProtocol
	}
	refresh := answer.Refresh
	if refresh == "" {
		refresh = fallbackRefresh
	}
	if refresh != "" && !HeaderCredential(refresh, 16384) {
		return nil, ErrAccountProtocol
	}
	var expiresAt int64
	var date string
	if common.Unmarshal(answer.Expires, &date) == nil && date != "" {
		parsed, err := time.Parse(time.RFC3339Nano, date)
		if err != nil {
			return nil, ErrAccountProtocol
		}
		expiresAt = parsed.Unix()
	} else {
		var milliseconds int64
		if common.Unmarshal(answer.Expires, &milliseconds) != nil || milliseconds <= 0 {
			return nil, ErrAccountProtocol
		}
		expiresAt = milliseconds / 1000
	}
	if expiresAt <= time.Now().Unix() {
		return nil, ErrDesktopLoginExpired
	}
	return &ClineCredential{Token: answer.Access, RefreshToken: refresh, UserID: user, ExpiresAt: expiresAt}, nil
}

// clinePayload handles only the documented optional {success,data} envelope.
// A false success value must never be accepted as a successful token response.
func clinePayload(raw []byte) ([]byte, error) {
	var envelope struct {
		Success *bool             `json:"success"`
		Data    common.RawMessage `json:"data"`
	}
	if common.Unmarshal(raw, &envelope) != nil {
		return nil, ErrAccountProtocol
	}
	if envelope.Success != nil && !*envelope.Success {
		return nil, ErrAccountProtocol
	}
	if envelope.Success != nil || len(envelope.Data) != 0 {
		if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
			return nil, ErrAccountProtocol
		}
		return envelope.Data, nil
	}
	return raw, nil
}

// RenewClineCredential performs exactly one rotation. The caller must serialize
// per account and durably retain the new pair before using it; never retry a
// possibly spent refresh token after a timeout or persistence failure. No DB
// transaction or persistence operation is owned by this transport service.
func RenewClineCredential(ctx context.Context, proxy string, credential *ClineCredential) (*ClineCredential, error) {
	if credential == nil || !HeaderCredential(credential.UserID, 256) {
		return nil, ErrAccountProtocol
	}
	if credential.RefreshToken == "" {
		return nil, ErrDesktopLoginExpired
	}
	if !HeaderCredential(credential.RefreshToken, 16384) {
		return nil, ErrAccountProtocol
	}
	body, err := common.Marshal(map[string]string{"refreshToken": credential.RefreshToken, "grantType": "refresh_token"})
	if err != nil {
		return nil, ErrAccountProtocol
	}
	status, raw, err := clineRequest(ctx, proxy, ClineAPIBase+"/auth/refresh", body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, ErrDesktopLoginExpired
	}
	if status == http.StatusBadRequest {
		// WorkOS' structured invalid_grant may be nested in Cline's envelope.
		// Classification never renders provider descriptions or token material.
		var failure struct {
			Error common.RawMessage `json:"error"`
		}
		if common.Unmarshal(raw, &failure) == nil {
			var code string
			if common.Unmarshal(failure.Error, &code) != nil {
				var detail struct {
					Code  string `json:"code"`
					Error string `json:"error"`
				}
				if common.Unmarshal(failure.Error, &detail) == nil {
					code = detail.Code
					if code == "" {
						code = detail.Error
					}
				}
			}
			switch code {
			case "invalid_grant", "invalid_token", "expired_token", "revoked_token", "unauthorized":
				return nil, ErrDesktopLoginExpired
			}
		}
	}
	if status != http.StatusOK {
		return nil, ErrAccountUpstream
	}
	next, err := clineParseCredential(raw, credential.RefreshToken, credential.UserID)
	if err != nil {
		return nil, err
	}
	next.Egress = credential.Egress
	return next, nil
}

var clineRecommendedID = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*$`)

// FetchClineModels discovers provider IDs from public feeds, never from a
// bundled fallback or an invented /models or identity endpoint. These feeds are
// catalogs, NOT proof of account entitlement, subscription, balance, or access.
func FetchClineModels(ctx context.Context, proxy string, credential *ClineCredential) ([]string, error) {
	if credential == nil || !HeaderCredential(credential.Token, 16384) || !HeaderCredential(credential.UserID, 256) {
		return nil, ErrAccountProtocol
	}
	if credential.ExpiresAt <= time.Now().Unix() {
		return nil, ErrDesktopLoginExpired
	}
	status, raw, err := clineRequest(ctx, proxy, clineRecommendedEndpoint, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, ErrAccountUpstream
	}
	payload, err := clinePayload(raw)
	if err != nil {
		return nil, err
	}
	var feed struct {
		Recommended []common.RawMessage `json:"recommended"`
		Free        []common.RawMessage `json:"free"`
		ClinePass   []common.RawMessage `json:"clinePass"`
	}
	if common.Unmarshal(payload, &feed) != nil {
		return nil, ErrAccountProtocol
	}
	models := make([]string, 0)
	seen := make(map[string]bool)
	for _, group := range [][]common.RawMessage{feed.Recommended, feed.Free, feed.ClinePass} {
		for _, entry := range group {
			var id string
			if common.Unmarshal(entry, &id) != nil {
				var object struct {
					ID           string `json:"id"`
					Model        string `json:"model"`
					ModelID      string `json:"modelId"`
					SnakeModelID string `json:"model_id"`
					Key          string `json:"key"`
					Slug         string `json:"slug"`
				}
				if common.Unmarshal(entry, &object) != nil {
					continue
				}
				for _, candidate := range []string{object.ID, object.Model, object.ModelID, object.SnakeModelID, object.Key, object.Slug} {
					if strings.TrimSpace(candidate) != "" {
						id = strings.TrimSpace(candidate)
						break
					}
				}
			}
			id = strings.TrimSpace(id)
			if len(id) > 256 || !clineRecommendedID.MatchString(id) || seen[id] {
				continue
			}
			seen[id] = true
			models = append(models, id)
		}
	}
	// The cloud catalog is optional in the pinned client. Keep only provider
	// IDs actually obtained from a successful response, and never auth headers.
	status, raw, err = clineRequest(ctx, proxy, clineModelsEndpoint, nil)
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil && status == http.StatusOK {
		payload := raw
		if len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] != '[' {
			payload, err = clinePayload(raw)
		}
		var cloud []struct {
			ID string `json:"id"`
		}
		if err == nil && common.Unmarshal(payload, &cloud) == nil {
			for _, item := range cloud {
				id := strings.TrimSpace(item.ID)
				if !HeaderCredential(id, 256) || seen[id] {
					continue
				}
				seen[id] = true
				models = append(models, id)
			}
		}
	}
	if len(models) == 0 {
		return nil, ErrAccountProtocol
	}
	return models, nil
}
