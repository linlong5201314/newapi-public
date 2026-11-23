package service

// Protocol: magpie-community/plugins@e6c457112ababf96d9415fe0a217ab0b7429ae98,
// packages/raccoon/index.mjs. Returned URLs are parsed by the controller; this
// service accepts only a claimed one-shot authorization code, never a URL.
import (
	"bytes"
	"context"
	"encoding/base64"
	"math/big"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
)

const RaccoonOrigin = "https://xiaohuanxiong.com"
const RaccoonAPIBase = RaccoonOrigin + "/api/web/llm/v2"

// RaccoonCredential is stored only inside an encrypted flow/channel key. Egress
// is filled by the session-bound controller and must be resolved on every relay
// or renewal; accepting a provider token must not change the owner's route.
type RaccoonCredential struct {
	Token        string        `json:"token"`
	RefreshToken string        `json:"refresh_token"`
	UserID       string        `json:"user_id"`
	ExpiresAt    int64         `json:"expires_at"`
	Egress       AccountEgress `json:"egress"`
}

type raccoonTokenResponse struct {
	Code *int `json:"code"`
	Data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	} `json:"data"`
}

func raccoonSuccess(code *int) bool {
	return code == nil || *code == 0 || *code == http.StatusOK
}

func raccoonToken(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) >= 7 && strings.EqualFold(value[:7], "Bearer ") {
		value = value[7:]
	}
	return value, value != "" && len(value) <= 32768 && !strings.ContainsFunc(value, unicode.IsSpace) && !strings.ContainsFunc(value, unicode.IsControl)
}

// Claims are read only from a token just returned by the fixed provider code
// exchange/refresh endpoint, not from client input. user_info subsequently proves
// it is usable at that provider; no unsigned client JWT is treated as authority.
func raccoonIssuedIdentity(token string) (string, int64, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
		return "", 0, ErrAccountProtocol
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		SID    string `json:"sid"`
		ISS    string `json:"iss"`
		Sub    string `json:"sub"`
		ID     string `json:"id"`
		UserID string `json:"user_id"`
		Exp    int64  `json:"exp"`
	}
	if err != nil || common.Unmarshal(raw, &claims) != nil || claims.Exp <= time.Now().Unix() {
		return "", 0, ErrAccountProtocol
	}
	var uid string
	for _, candidate := range []string{claims.SID, claims.ISS} {
		hex := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(candidate), "0x"), "0X")
		if hex == "" || len(hex) > 256 || strings.ContainsFunc(hex, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') }) {
			continue
		}
		if number, ok := new(big.Int).SetString(hex, 16); ok {
			uid = number.String()
			break
		}
	}
	if uid == "" {
		for _, candidate := range []string{claims.Sub, claims.ID, claims.UserID} {
			if candidate = strings.TrimSpace(candidate); candidate != "" {
				uid = candidate
				break
			}
		}
	}
	if uid == "" || len(uid) > 256 || strings.ContainsFunc(uid, unicode.IsControl) {
		return "", 0, ErrAccountProtocol
	}
	return uid, claims.Exp, nil
}

func verifyRaccoonCredential(ctx context.Context, proxy string, credential *RaccoonCredential) error {
	uid, expires, err := raccoonIssuedIdentity(credential.Token)
	if err != nil || credential.UserID != "" && credential.UserID != uid {
		return ErrAccountProtocol
	}
	request, err := http.NewRequest(http.MethodGet, RaccoonOrigin+"/api/web/auth/v1/user_info", nil)
	if err != nil {
		return ErrAccountProtocol
	}
	request.Header.Set("Authorization", "Bearer "+credential.Token)
	var response struct {
		Code *int           `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := accountAuthJSON(ctx, proxy, request, &response); err != nil {
		return err
	}
	if !raccoonSuccess(response.Code) || len(response.Data) == 0 {
		return ErrAccountProtocol
	}
	// Some releases return identity here, others only a phone/account profile.
	// If present it must agree with the provider-issued token's account.
	for _, key := range []string{"user_id", "userId", "uid", "id"} {
		if value, ok := response.Data[key].(string); ok && value != "" && value != uid {
			return ErrAccountProtocol
		}
	}
	credential.UserID, credential.ExpiresAt = uid, expires
	return nil
}

// RaccoonLiveModels returns only identifiers actually present in the successful
// provider response. In particular it never inserts Magpie's synthetic default
// or static fallback catalog. Empty/malformed catalogs fail closed.
func RaccoonLiveModels(ctx context.Context, proxy, token string) ([]string, error) {
	request, err := http.NewRequest(http.MethodGet, RaccoonAPIBase+"/model_catalog", nil)
	if err != nil {
		return nil, ErrAccountProtocol
	}
	request.Header.Set("Authorization", "Bearer "+token)
	type category struct {
		Models []struct {
			Name      string `json:"name"`
			ModelName string `json:"model_name"`
		} `json:"models"`
	}
	var response struct {
		Code *int `json:"code"`
		Data *struct {
			Categories []category `json:"categories"`
		} `json:"data"`
		Categories []category `json:"categories"`
	}
	if err := accountAuthJSON(ctx, proxy, request, &response); err != nil {
		return nil, err
	}
	if !raccoonSuccess(response.Code) {
		return nil, ErrAccountProtocol
	}
	categories := response.Categories
	if response.Data != nil {
		categories = response.Data.Categories
	}
	models := make([]string, 0)
	seen := make(map[string]bool)
	for _, category := range categories {
		for _, entry := range category.Models {
			id := strings.TrimSpace(entry.Name)
			if id == "" {
				id = strings.TrimSpace(entry.ModelName)
			}
			if id == "" || len(id) > 256 || strings.ContainsAny(id, ",\r\n\t") || strings.ContainsFunc(id, unicode.IsControl) {
				return nil, ErrAccountProtocol
			}
			if !seen[id] {
				seen[id] = true
				models = append(models, id)
			}
			if len(models) > 2048 {
				return nil, ErrAccountProtocol
			}
		}
	}
	if len(models) == 0 {
		return nil, ErrAccountProtocol
	}
	return models, nil
}

func CompleteRaccoonAuthorization(ctx context.Context, proxy, code string) (*RaccoonCredential, []string, error) {
	if code == "" || len(code) > 4096 || strings.ContainsFunc(code, unicode.IsControl) {
		return nil, nil, ErrAccountProtocol
	}
	body, err := common.Marshal(map[string]string{"authorization_code": code})
	if err != nil {
		return nil, nil, ErrAccountProtocol
	}
	request, err := http.NewRequest(http.MethodPost, RaccoonOrigin+"/api/web/auth/v1/login_with_authorization_code", bytes.NewReader(body))
	if err != nil {
		return nil, nil, ErrAccountProtocol
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	var response raccoonTokenResponse
	if err := accountAuthJSON(ctx, proxy, request, &response); err != nil {
		return nil, nil, err
	}
	access, accessOK := raccoonToken(response.Data.AccessToken)
	refresh, refreshOK := raccoonToken(response.Data.RefreshToken)
	if !raccoonSuccess(response.Code) || !accessOK || !refreshOK {
		return nil, nil, ErrAccountProtocol
	}
	credential := &RaccoonCredential{Token: access, RefreshToken: refresh}
	if err := verifyRaccoonCredential(ctx, proxy, credential); err != nil {
		return nil, nil, err
	}
	models, err := RaccoonLiveModels(ctx, proxy, credential.Token)
	if err != nil {
		return nil, nil, err
	}
	return credential, models, nil
}

// VerifyRaccoonImportedCredential validates one existing desktop/Magpie session
// against the provider before the session-bound import flow can create a channel.
func VerifyRaccoonImportedCredential(ctx context.Context, proxy string, credential *RaccoonCredential) error {
	return verifyRaccoonCredential(ctx, proxy, credential)
}

// RefreshRaccoonAccount preserves identity and stable egress. Callers must
// resolve Egress before invoking it and persist the rotated encrypted credential
// atomically; no credential or upstream error body belongs in an API response.
func RefreshRaccoonAccount(ctx context.Context, proxy string, prior *RaccoonCredential) (*RaccoonCredential, error) {
	if prior == nil || prior.UserID == "" {
		return nil, ErrAccountProtocol
	}
	refresh, ok := raccoonToken(prior.RefreshToken)
	if !ok {
		return nil, ErrAccountProtocol
	}
	body, err := common.Marshal(map[string]string{"refresh_token": refresh})
	if err != nil {
		return nil, ErrAccountProtocol
	}
	request, err := http.NewRequest(http.MethodPost, RaccoonOrigin+"/api/web/auth/v1/refresh", bytes.NewReader(body))
	if err != nil {
		return nil, ErrAccountProtocol
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	var response raccoonTokenResponse
	if err := accountAuthJSON(ctx, proxy, request, &response); err != nil {
		return nil, err
	}
	access, ok := raccoonToken(response.Data.AccessToken)
	if !raccoonSuccess(response.Code) || !ok {
		return nil, ErrAccountProtocol
	}
	if response.Data.RefreshToken != "" {
		var valid bool
		refresh, valid = raccoonToken(response.Data.RefreshToken)
		if !valid {
			return nil, ErrAccountProtocol
		}
	}
	credential := &RaccoonCredential{Token: access, RefreshToken: refresh, UserID: prior.UserID, Egress: prior.Egress}
	if err := verifyRaccoonCredential(ctx, proxy, credential); err != nil {
		return nil, err
	}
	return credential, nil
}
