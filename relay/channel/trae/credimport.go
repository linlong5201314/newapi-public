// credimport.go parses the official Trae CN loopback callback. It does not
// implement an OAuth authorization-code or hosted redirect flow.
package trae

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	DomesticAuthHost = "https://api.trae.cn"
	IDEClientID      = "ono9krqynydwx5"
)

// CallbackInfo is secret authentication material; never include it in API
// responses or logs. The expiry is only a refresh hint, not JWT verification.
type CallbackInfo struct {
	RefreshToken string
	AccessToken  string
	UID          string
	Nickname     string
	EnterpriseID string
	ExpiresAt    int64
	ClientID     string
	AuthHost     string
}

func parseJSONParam(raw string) (map[string]json.RawMessage, error) {
	if raw == "" {
		return nil, nil
	}
	// ParseQuery has already performed URL decoding. Decode JSON once, and
	// retain numbers verbatim so large provider user IDs never lose precision.
	var object map[string]json.RawMessage
	if common.Unmarshal([]byte(raw), &object) != nil || object == nil {
		return nil, errors.New("invalid Trae callback JSON")
	}
	return object, nil
}

// callbackString accepts only the scalar forms present in the official callback.
// Conflicting aliases are rejected rather than letting ambiguous input choose an
// account, client, or token.
func callbackString(object map[string]json.RawMessage, keys ...string) (string, error) {
	var value string
	for _, key := range keys {
		item := object[key]
		if len(item) == 0 || string(item) == "null" {
			continue
		}
		var text string
		if item[0] == '"' {
			if common.Unmarshal(item, &text) != nil {
				return "", errors.New("invalid Trae callback field")
			}
		} else if _, err := strconv.ParseInt(string(item), 10, 64); err == nil {
			text = string(item)
		} else {
			return "", errors.New("invalid Trae callback field")
		}
		if value != "" && text != "" && value != text {
			return "", errors.New("conflicting Trae callback fields")
		}
		if text != "" {
			value = text
		}
	}
	return value, nil
}

func callbackExpiry(value string) int64 {
	if number, err := strconv.ParseInt(value, 10, 64); err == nil {
		return NormalizeExpire(number)
	}
	if instant, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return instant.Unix()
	}
	return 0
}

// CallbackTokenExpiry reads only a scheduling hint. Callers MUST authenticate the
// access token with the provider before accepting it; this does not verify a JWT.
func CallbackTokenExpiry(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var payload struct {
		ExpiresAt json.Number `json:"exp"`
	}
	if common.Unmarshal(raw, &payload) != nil {
		return 0
	}
	return callbackExpiry(payload.ExpiresAt.String())
}

// ParseCallback accepts the actual Trae CN IDE/SOLO local-client callback:
// http://127.0.0.1:<port>/authorize?userJwt={...}&userInfo={...}.
// A legacy refreshToken-only callback is also supported. No callback URL or
// callback-supplied endpoint is fetched. Both token fields must be retained: the
// page's valid pair is usable as-is and must not be rotated under another client.
func ParseCallback(rawURL string) (*CallbackInfo, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || len(rawURL) > 65536 {
		return nil, errors.New("invalid Trae callback URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "/authorize" || u.RawPath != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("unsupported Trae local callback URI")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("invalid Trae callback port")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("invalid Trae callback query")
	}
	for _, values := range query {
		if len(values) != 1 {
			return nil, errors.New("duplicate Trae callback parameter")
		}
	}
	if query.Get("error") != "" || query.Get("code") != "" {
		return nil, errors.New("Trae callback did not provide a local credential")
	}
	jwt, err := parseJSONParam(query.Get("userJwt"))
	if err != nil {
		return nil, err
	}
	account, err := parseJSONParam(query.Get("userInfo"))
	if err != nil {
		return nil, err
	}
	info := &CallbackInfo{ClientID: IDEClientID, AuthHost: DomesticAuthHost}
	info.AccessToken, err = callbackString(jwt, "Token", "token")
	if err != nil {
		return nil, err
	}
	info.RefreshToken, err = callbackString(jwt, "RefreshToken", "refreshToken")
	if err != nil {
		return nil, err
	}
	if info.RefreshToken == "" {
		info.RefreshToken = query.Get("refreshToken")
	}
	info.UID, err = callbackString(account, "UserID", "userId")
	if err != nil {
		return nil, err
	}
	if userID := query.Get("userId"); userID != "" {
		if info.UID != "" && info.UID != userID {
			return nil, errors.New("conflicting Trae callback account")
		}
		info.UID = userID
	}
	info.Nickname, err = callbackString(account, "ScreenName", "screenName")
	if err != nil {
		return nil, err
	}
	info.EnterpriseID, err = callbackString(account, "TenantID", "tenantId", "EnterpriseID", "enterpriseId")
	if err != nil {
		return nil, err
	}
	client, err := callbackString(jwt, "ClientID", "clientId")
	if err != nil {
		return nil, err
	}
	if named := query.Get("clientId"); named != "" {
		if client != "" && client != named {
			return nil, errors.New("conflicting Trae callback client")
		}
		client = named
	}
	if client != "" {
		if client != IDEClientID && client != ClientID {
			return nil, errors.New("unsupported Trae CN client")
		}
		info.ClientID = client
	}
	expiry, err := callbackString(jwt, "TokenExpireAt", "tokenExpireAt")
	if err != nil {
		return nil, err
	}
	info.ExpiresAt = callbackExpiry(expiry)
	if info.ExpiresAt <= 0 {
		info.ExpiresAt = CallbackTokenExpiry(info.AccessToken)
	}
	// A global callback must never be silently relabelled CN. These values
	// are validation evidence only, never caller-selected network targets.
	for _, key := range []string{"realm", "domain"} {
		if value := strings.ToLower(query.Get(key)); value != "" && value != "cn" && value != "trae.cn" {
			return nil, errors.New("only domestic Trae authorization is supported")
		}
	}
	for _, key := range []string{"Region", "region", "AIRegion", "aiRegion"} {
		value, err := callbackString(account, key)
		if err != nil || value != "" && !strings.EqualFold(value, "CN") {
			return nil, errors.New("unsupported Trae account region")
		}
	}
	for _, value := range []string{query.Get("authHost"), query.Get("auth_host"), query.Get("host")} {
		if err := validateDomesticCallbackHost(value); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"AuthHost", "authHost", "Host", "host"} {
		value, err := callbackString(account, key)
		if err != nil {
			return nil, err
		}
		if err := validateDomesticCallbackHost(value); err != nil {
			return nil, err
		}
	}
	if info.RefreshToken == "" && info.AccessToken == "" {
		return nil, errors.New("Trae callback is missing credentials")
	}
	return info, nil
}

func validateDomesticCallbackHost(raw string) error {
	if raw == "" {
		return nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == "" {
		host := strings.ToLower(u.Host)
		if host == "trae.cn" || strings.HasSuffix(host, ".trae.cn") || host == "api.trae.com.cn" || host == "trae-api-cn.mchost.guru" {
			return nil
		}
	}
	return errors.New("unsupported Trae domestic callback host")
}

// NewDeviceIDs supplies stable identifiers when a manual import has none.
func NewDeviceIDs() (machineID, deviceID string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b[:16]), hex.EncodeToString(b[16:])
}

// ImportCredential is the legacy relay helper. Dashboard imports use the
// session-bound controller flow and AccountAuthorizationJSON instead.
func ImportCredential(info *CallbackInfo, machineID, deviceID, baseOverride string) (*Cred, error) {
	if machineID == "" || deviceID == "" {
		machineID, deviceID = NewDeviceIDs()
	}
	if baseOverride == "" {
		baseOverride = envBaseOverride()
	}
	cred := &Cred{UID: info.UID, Nickname: info.Nickname, EnterpriseID: info.EnterpriseID,
		MachineID: machineID, DeviceID: deviceID, Domain: "trae.cn", ExpiresAt: info.ExpiresAt,
		ClientID: info.ClientID, ApiHost: info.AuthHost, BaseOverride: baseOverride}
	client := NewClient(cred)
	if info.RefreshToken != "" {
		cred.RefreshToken = info.RefreshToken
		next, err := client.refreshTokenOnce()
		if err != nil {
			return nil, fmt.Errorf("ExchangeToken failed: %w", err)
		}
		cred = next
	} else if info.AccessToken != "" {
		cred.AccessToken = info.AccessToken
	} else {
		return nil, errors.New("no refreshToken or accessToken to import")
	}
	if uid, nick, ent, err := NewClient(cred).GetUserInfo(); err == nil {
		if cred.UID == "" {
			cred.UID = uid
		}
		if cred.Nickname == "" {
			cred.Nickname = nick
		}
		if cred.EnterpriseID == "" {
			cred.EnterpriseID = ent
		}
	}
	if cred.ExpiresAt <= 0 && cred.RefreshToken == "" {
		cred.ExpiresAt = time.Now().Add(time.Hour).Unix()
	}
	return cred, nil
}

func ImportFromRefreshToken(refreshToken, uid, machineID, deviceID, baseOverride string) (*Cred, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, errors.New("refreshToken is required")
	}
	return ImportCredential(&CallbackInfo{RefreshToken: refreshToken, UID: strings.TrimSpace(uid)}, machineID, deviceID, baseOverride)
}
