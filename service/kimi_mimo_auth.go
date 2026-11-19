package service

// Verified against magpie-community/plugins@62645eceb78daf84e95e41acb1ddaf02b403daae.
// These are desktop account protocols, not public OAuth clients or API keys.
import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"golang.org/x/net/publicsuffix"
)

const KimiWorkBase = "https://agent-gw.kimi.com/coding/v1"
const MiMoAccount = "https://account.xiaomi.com"
const miMoUA = "miNative PC/Normal Windows_NT/10.0.26100 SDKV/1.0.0 DEVT/PC DEVS/Windows APP/miaccount_desktop APPV/0.1.0"
const MiMoAppVersion = "26.929.292248"

var ErrDesktopLoginExpired = errors.New("The provider login expired; sign in again")
var ErrMiMoRegion = errors.New("MiMo does not serve this Xiaomi account region")

// AccountEgress is stored only inside encrypted credentials or encrypted flows.
// Endpoint identity survives runtime listener changes; the endpoint configuration
// must not change between login, renewal and relay.
type AccountEgress struct {
	Key         string `json:"key"`
	Proxy       string `json:"proxy"`
	EndpointID  int    `json:"endpoint_id"`
	Fingerprint string `json:"fingerprint"`
}

func (e AccountEgress) Resolve(ownerID, channelID int, settings dto.ChannelSettings) (string, error) {
	if e.Key == "" || ownerID <= 0 {
		return "", ErrAccountEgress
	}
	proxy, err := model.ResolveStableChannelProxy(ownerID, channelID, e.Key, settings)
	if err != nil {
		return "", ErrAccountEgress
	}
	if e.EndpointID > 0 {
		id, fp, err := model.ResolveProxyEndpointIdentity(ownerID, proxy)
		if err != nil || id != e.EndpointID || fp != e.Fingerprint {
			return "", ErrAccountEgress
		}
	} else if proxy != e.Proxy {
		return "", ErrAccountEgress
	}
	return proxy, nil
}

type KimiWorkCredential struct {
	Source    string        `json:"source"`
	WorkToken string        `json:"workToken"`
	UserID    string        `json:"userId"`
	DeviceID  string        `json:"deviceId,omitempty"`
	Egress    AccountEgress `json:"egress"`
}

type MiMoCredential struct {
	UserID    string            `json:"userId"`
	CUserID   string            `json:"cUserId,omitempty"`
	PassToken string            `json:"passToken"`
	DeviceID  string            `json:"deviceId"`
	Region    string            `json:"region"`
	Cookies   map[string]string `json:"cookies"`
	ExpiresAt int64             `json:"expiresAt"`
	Egress    AccountEgress     `json:"egress"`
}

type MiMoTicket struct {
	PollURL  string `json:"pollUrl"`
	DeviceID string `json:"deviceId"`
	Until    int64  `json:"until"`
}

func MiMoBase(region string) string {
	switch region {
	case "SGP":
		return "https://mimo-server-sgp.xiaomimimo.com/api"
	case "RU":
		return "https://mimo-server-ru.xiaomimimo.com/api"
	case "IN":
		return "https://mimo-server-in.xiaomimimo.com/api"
	default:
		return ""
	}
}

// HeaderCredential rejects separators rather than silently dropping credential bytes.
func HeaderCredential(v string, limit int) bool {
	if len(v) == 0 || len(v) > limit {
		return false
	}
	for _, b := range []byte(v) {
		if b < 33 || b > 126 || b == ';' || b == ',' {
			return false
		}
	}
	return true
}

func KimiWorkHeaders(credential KimiWorkCredential) http.Header {
	h := make(http.Header)
	h.Set("Authorization", "Bearer "+credential.WorkToken)
	h.Set("User-Agent", "kimi-code-cli/0.36.0")
	h.Set("X-Msh-Platform", "kimi-code-cli")
	h.Set("X-Msh-Version", "0.36.0")
	h.Set("X-Msh-Daimon-Version", "0.6.10")
	if credential.DeviceID != "" {
		h.Set("X-Msh-Device-Id", credential.DeviceID)
	}
	return h
}

func MiMoHeaders() http.Header {
	h := make(http.Header)
	h.Set("User-Agent", miMoUA)
	h.Set("X-Client-Version", MiMoAppVersion)
	return h
}

// Requests never follow provider-controlled redirects implicitly; callers must
// validate every destination before sending any credential. Body/errors are bounded
// and never rendered or logged, because polling URLs contain bearer tickets.
func desktopAccountRequest(ctx context.Context, proxy, target string, headers http.Header) (int, http.Header, []byte, error) {
	client, err := GetAccountAuthHTTPClient(proxy, "")
	if err != nil || client == nil {
		return 0, nil, nil, ErrAccountEgress
	}
	copyClient := *client
	copyClient.Timeout = 8 * time.Second
	copyClient.Jar = nil
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, nil, ErrAccountProtocol
	}
	req.Header = headers.Clone()
	res, err := copyClient.Do(req)
	if err != nil {
		return 0, nil, nil, ErrAccountUpstream
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return 0, nil, nil, ErrAccountProtocol
	}
	return res.StatusCode, res.Header, raw, nil
}

func ValidateKimiWork(ctx context.Context, proxy string, cred KimiWorkCredential) ([]string, error) {
	if cred.Source != "kimi-work-desktop" || !HeaderCredential(cred.WorkToken, 8192) || !HeaderCredential(cred.UserID, 256) || (cred.DeviceID != "" && !HeaderCredential(cred.DeviceID, 128)) {
		return nil, ErrAccountProtocol
	}
	status, _, raw, err := desktopAccountRequest(ctx, proxy, KimiWorkBase+"/models", KimiWorkHeaders(cred))
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		return nil, ErrDesktopLoginExpired
	}
	if status != http.StatusOK {
		return nil, ErrAccountUpstream
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		HasMore bool `json:"has_more"`
	}
	if common.Unmarshal(raw, &result) != nil || result.HasMore || len(result.Data) > 100 {
		return nil, ErrAccountProtocol
	}
	models := make([]string, 0, len(result.Data))
	seen := map[string]bool{}
	for _, item := range result.Data {
		if !HeaderCredential(item.ID, 256) || strings.ContainsAny(item.ID, "/\\") || seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		models = append(models, item.ID)
	}
	if len(models) == 0 {
		return nil, ErrAccountProtocol
	}
	return models, nil
}

func miMoURL(target string, hosts ...string) (*url.URL, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, ErrAccountProtocol
	}
	for _, host := range hosts {
		if u.Host == host {
			return u, nil
		}
	}
	return nil, ErrAccountProtocol
}

// Destination diagnostics exclude paths, queries, userinfo and fragment contents.
func miMoDestinationMetadata(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return "parse_error=true"
	}
	host := u.Hostname()
	if len(host) == 0 || len(host) > 253 || strings.Trim(host, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-") != "" {
		host = "redacted"
	}
	scheme := u.Scheme
	if scheme != "http" && scheme != "https" {
		scheme = "other"
	}
	return fmt.Sprintf("scheme=%s host=%s port=%t userinfo=%t fragment=%t opaque=%t", scheme, host, u.Port() != "", u.User != nil, u.Fragment != "", u.Opaque != "")
}

func StartMiMoSignIn(ctx context.Context, proxy string) (ticket MiMoTicket, authURL string, resultErr error) {
	stage, upstreamStatus := "device", 0
	defer func() {
		if resultErr != nil {
			// Fixed stage and HTTP status only: URLs and errors can contain tickets.
			common.SysLog(fmt.Sprintf("MiMo login start failed: stage=%s upstream_http=%d", stage, upstreamStatus))
		}
	}()
	device := make([]byte, 16)
	if _, err := rand.Read(device); err != nil {
		return ticket, "", err
	}
	ticket.DeviceID = "pc_" + hex.EncodeToString(device)
	base := MiMoBase("SGP")
	stage = "service_request"
	status, headers, _, err := desktopAccountRequest(ctx, proxy, base+"/user/xiaomi/me", MiMoHeaders())
	upstreamStatus = status
	if err != nil {
		return ticket, "", err
	}
	stage = "service_status"
	if status < 300 || status >= 400 {
		return ticket, "", ErrAccountProtocol
	}
	stage = "service_redirect"
	login, err := miMoURL(headers.Get("Location"), "account.xiaomi.com")
	if err != nil {
		return ticket, "", err
	}
	stage = "service_callback"
	sid, callback := login.Query().Get("sid"), login.Query().Get("callback")
	callbackURL, err := url.Parse(callback)
	if err != nil || callbackURL.Host != "mimo-server-sgp.xiaomimimo.com" || callbackURL.User != nil || callbackURL.Path != "/api/sts" || (callbackURL.Scheme != "http" && callbackURL.Scheme != "https") || callbackURL.Fragment != "" || sid != "mimosgp" {
		return ticket, "", ErrAccountProtocol
	}
	callbackURL.Scheme = "https"
	q := url.Values{"_group": {"DEFAULT"}, "_qrsize": {"240"}, "qs": {"%3Fsid%3D" + url.QueryEscape(sid) + "%26_json%3Dtrue"}, "callback": {callbackURL.String()}, "_hasLogo": {"false"}, "sid": {sid}, "serviceParam": {""}, "_locale": {"en_US"}}
	h := MiMoHeaders()
	h.Set("Cookie", "deviceId="+ticket.DeviceID+"; pass_ua=pc; uLocale=zh_CN")
	stage = "ticket_request"
	status, _, raw, err := desktopAccountRequest(ctx, proxy, MiMoAccount+"/longPolling/loginUrl?"+q.Encode(), h)
	upstreamStatus = status
	if err != nil {
		return ticket, "", err
	}
	var result struct {
		Code     *int   `json:"code"`
		LoginURL string `json:"loginUrl"`
		PollURL  string `json:"lp"`
		Timeout  int    `json:"timeout"`
	}
	stage = "ticket_response"
	if status != http.StatusOK || common.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(string(raw)), "&&&START&&&")), &result) != nil || result.Code == nil || *result.Code != 0 {
		return ticket, "", ErrAccountProtocol
	}
	stage = "login_destination"
	if _, err := miMoURL(result.LoginURL, "account.xiaomi.com", "ak.account.xiaomi.com", "sgp.account.xiaomi.com"); err != nil {
		common.SysLog("MiMo rejected login destination: " + miMoDestinationMetadata(result.LoginURL))
		return ticket, "", err
	}
	stage = "poll_destination"
	if _, err := miMoURL(result.PollURL, "account.xiaomi.com", "ak.lp.account.xiaomi.com", "sgp.lp.account.xiaomi.com"); err != nil {
		common.SysLog("MiMo rejected polling destination: " + miMoDestinationMetadata(result.PollURL))
		return ticket, "", err
	}
	stage = "ticket_size"
	if len(result.LoginURL) > 8192 || len(result.PollURL) > 8192 {
		return ticket, "", ErrAccountProtocol
	}
	life := 600
	if result.Timeout > 0 {
		life = min(life, result.Timeout)
	}
	ticket.Until = time.Now().Add(time.Duration(life) * time.Second).Unix()
	ticket.PollURL = result.PollURL
	return ticket, result.LoginURL, nil
}

func PollMiMoSignIn(ctx context.Context, proxy string, ticket MiMoTicket) (*MiMoCredential, error) {
	if time.Now().Unix() >= ticket.Until {
		return nil, model.ErrAuthFlowExpired
	}
	if _, err := miMoURL(ticket.PollURL, "account.xiaomi.com", "ak.lp.account.xiaomi.com", "sgp.lp.account.xiaomi.com"); err != nil || !HeaderCredential(ticket.DeviceID, 128) {
		return nil, ErrAccountProtocol
	}
	pollCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	h := MiMoHeaders()
	h.Set("Cookie", "deviceId="+ticket.DeviceID+"; pass_ua=pc; uLocale=zh_CN")
	status, _, raw, err := desktopAccountRequest(pollCtx, proxy, ticket.PollURL, h)
	if err != nil {
		if errors.Is(pollCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, nil
		}
		return nil, err
	}
	if status != http.StatusOK {
		return nil, ErrAccountUpstream
	}
	var result struct {
		PassToken string          `json:"passToken"`
		UserID    json.RawMessage `json:"userId"`
		CUserID   string          `json:"cUserId"`
	}
	if common.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(string(raw)), "&&&START&&&")), &result) != nil {
		return nil, ErrAccountProtocol
	}
	if result.PassToken == "" {
		return nil, nil
	}
	userID, idErr := miMoUserID(result.UserID)
	if idErr != nil || !HeaderCredential(userID, 256) || !HeaderCredential(result.PassToken, 8192) || (result.CUserID != "" && !HeaderCredential(result.CUserID, 256)) {
		return nil, ErrAccountProtocol
	}
	return &MiMoCredential{UserID: userID, CUserID: result.CUserID, PassToken: result.PassToken, DeviceID: ticket.DeviceID, Region: "SGP"}, nil
}

// RenewMiMoSession follows only account.xiaomi.com and the chosen MiMo host.
// passToken is sent exclusively to Xiaomi Account, never to MiMo or another host.
func RenewMiMoSession(ctx context.Context, proxy string, cred MiMoCredential) (*MiMoCredential, error) {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return renewMiMoSession(bounded, proxy, cred, true)
}

func renewMiMoSession(ctx context.Context, proxy string, cred MiMoCredential, allowRegion bool) (*MiMoCredential, error) {
	base := MiMoBase(cred.Region)
	if base == "" || !HeaderCredential(cred.UserID, 256) || !HeaderCredential(cred.PassToken, 8192) || !HeaderCredential(cred.DeviceID, 128) {
		return nil, ErrAccountProtocol
	}
	bu, _ := url.Parse(base)
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, ErrAccountProtocol
	}
	account, _ := url.Parse(MiMoAccount)
	cookies := []*http.Cookie{{Name: "userId", Value: cred.UserID}, {Name: "passToken", Value: cred.PassToken}, {Name: "deviceId", Value: cred.DeviceID}, {Name: "pass_ua", Value: "pc"}, {Name: "uLocale", Value: "zh_CN"}}
	if cred.CUserID != "" {
		if !HeaderCredential(cred.CUserID, 256) {
			return nil, ErrAccountProtocol
		}
		cookies = append(cookies, &http.Cookie{Name: "cUserId", Value: cred.CUserID})
	}
	jar.SetCookies(account, cookies)
	target := base + "/user/xiaomi/me"
	for range 11 {
		at, err := miMoURL(target, "account.xiaomi.com", bu.Host)
		if err != nil {
			return nil, err
		}
		h := MiMoHeaders()
		request := &http.Request{Header: h}
		for _, cookie := range jar.Cookies(at) {
			request.AddCookie(cookie)
		}
		status, headers, raw, err := desktopAccountRequest(ctx, proxy, target, h)
		if err != nil {
			return nil, err
		}
		response := &http.Response{Header: headers}
		jar.SetCookies(at, response.Cookies())
		if status >= 300 && status < 400 {
			next, err := at.Parse(headers.Get("Location"))
			if err != nil {
				return nil, ErrAccountProtocol
			}
			if next.Scheme == "http" && next.Host == bu.Host {
				next.Scheme = "https"
			}
			if _, err := miMoURL(next.String(), "account.xiaomi.com", bu.Host); err != nil {
				return nil, err
			}
			target = next.String()
			continue
		}
		if at.Host != bu.Host || at.Path != "/api/user/xiaomi/me" {
			return nil, ErrDesktopLoginExpired
		}
		var result struct {
			Code *int `json:"code"`
			Data struct {
				UserID json.RawMessage `json:"userId"`
				Region string          `json:"region"`
			} `json:"data"`
		}
		if common.Unmarshal(raw, &result) != nil {
			return nil, ErrDesktopLoginExpired
		}
		if status == http.StatusForbidden || (result.Code != nil && (*result.Code == 403 || *result.Code == 46109)) {
			return nil, ErrMiMoRegion
		}
		gotID, idErr := miMoUserID(result.Data.UserID)
		if status != http.StatusOK || result.Code == nil || *result.Code != 0 || idErr != nil || gotID != cred.UserID {
			return nil, ErrDesktopLoginExpired
		}
		if r := strings.ToUpper(strings.TrimSpace(result.Data.Region)); r != "" && r != cred.Region {
			if !allowRegion || MiMoBase(r) == "" {
				return nil, ErrMiMoRegion
			}
			cred.Region = r
			return renewMiMoSession(ctx, proxy, cred, false)
		}
		cred.Cookies = map[string]string{}
		route, _ := url.Parse(base + "/route/chat/completions")
		for _, cookie := range jar.Cookies(route) {
			if cookie.Name == "passToken" || cookie.Name == "pass_ua" || !HeaderCredential(cookie.Name, 128) || !HeaderCredential(cookie.Value, 8192) {
				continue
			}
			cred.Cookies[cookie.Name] = cookie.Value
		}
		if len(cred.Cookies) == 0 {
			return nil, ErrAccountProtocol
		}
		cred.ExpiresAt = time.Now().Add(24 * time.Hour).Unix()
		return &cred, nil
	}
	return nil, ErrAccountProtocol
}

// Xiaomi identifiers are decimal strings or JSON integers, never float64.
func miMoUserID(raw json.RawMessage) (string, error) {
	value := strings.TrimSpace(string(raw))
	if len(value) > 0 && value[0] == 34 {
		var text string
		if common.Unmarshal(raw, &text) != nil {
			return "", ErrAccountProtocol
		}
		value = text
	}
	if len(value) == 0 || len(value) > 32 {
		return "", ErrAccountProtocol
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return "", ErrAccountProtocol
		}
	}
	return value, nil
}
