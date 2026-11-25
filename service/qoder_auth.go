package service

// Qoder device protocol verified against shuishuipingan/qoder2api-hub
// commit 25d05944cdc9b36cb69b162d9cba0d8e8989f4db (MIT), qoder_accounts.py.
// This implementation does not access desktop files, cookies, campaigns, or rewards.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// QoderTicket contains secret PKCE material. Persist only in an encrypted,
// owner/session-bound, single-use flow. Never return it to the browser.
type QoderTicket struct {
	Realm     string `json:"realm"`
	Nonce     string `json:"nonce"`
	Verifier  string `json:"verifier"`
	MachineID string `json:"machine_id"`
	Until     int64  `json:"until"`
	Interval  int    `json:"interval"`
}

// QoderCredential is an owner account credential, not a public API key.
// Token material and Egress must be encrypted together at rest by the caller.
type QoderCredential struct {
	Realm          string        `json:"realm"`
	UserID         string        `json:"user_id"`
	Token          string        `json:"token"`
	RefreshToken   string        `json:"refresh_token,omitempty"`
	ExpiresAt      int64         `json:"expires_at"`
	MachineID      string        `json:"machine_id"`
	Nickname       string        `json:"nickname,omitempty"`
	UserType       string        `json:"user_type,omitempty"`
	OrganizationID string        `json:"organization_id,omitempty"`
	Egress         AccountEgress `json:"egress"`
}

type qoderRealm struct {
	website, api, gateway, clientID, redirect string
}

func qoderRealmConfig(realm string) (qoderRealm, error) {
	switch realm {
	case "cn":
		return qoderRealm{"https://qoder.com.cn", "https://openapi.qoder.com.cn", "https://gateway.qoder.com.cn", "1c5e33e1-364d-4ce6-b02c-acaa81274a5c", "qoder-work-cn://"}, nil
	case "global":
		return qoderRealm{"https://qoder.com", "https://openapi.qoder.sh", "https://api3.qoder.sh", "e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb", ""}, nil
	default:
		return qoderRealm{}, ErrAccountProtocol
	}
}

// QoderGateway returns the verified gateway for the explicitly selected realm.
// It does not retry in another region or accept a provider-controlled host.
func QoderGateway(realm string) (string, error) {
	cfg, err := qoderRealmConfig(realm)
	return cfg.gateway, err
}

// StartQoderSignIn constructs Qoder's custom PKCE device grant URL locally.
// There is no upstream init endpoint and no arbitrary code/callback exchange.
func StartQoderSignIn(ctx context.Context, proxy, realm string) (*QoderTicket, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	cfg, err := qoderRealmConfig(realm)
	if err != nil {
		return nil, "", err
	}
	if _, err := qoderHTTPClient(proxy); err != nil {
		return nil, "", err
	}
	raw := make([]byte, 96)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", ErrAccountProtocol
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw[:48])
	// The provider nonce is UUIDv4: dashed in CN, compact globally.
	raw[54] = raw[54]&0x0f | 0x40
	raw[56] = raw[56]&0x3f | 0x80
	nonce := hex.EncodeToString(raw[48:64])
	if realm == "cn" {
		nonce = nonce[:8] + "-" + nonce[8:12] + "-" + nonce[12:16] + "-" + nonce[16:20] + "-" + nonce[20:]
	}
	ticket := &QoderTicket{Realm: realm, Nonce: nonce, Verifier: verifier, MachineID: hex.EncodeToString(raw[64:]), Until: time.Now().Add(10 * time.Minute).Unix(), Interval: 3}
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{"challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "challenge_method": {"S256"}, "nonce": {nonce}, "client_id": {cfg.clientID}, "machine_id": {ticket.MachineID}}
	if cfg.redirect != "" {
		q.Set("redirect_uri", cfg.redirect)
	}
	return ticket, cfg.website + "/device/selectAccounts?" + q.Encode(), nil
}

type qoderClient struct{ http *http.Client }

var qoderTransports = proxyHTTPClientCache{clients: make(map[string]*http.Client), aliases: make(map[string]string)}

// QoderHTTPClientFactory is replaced only by package-level protocol tests.
// Production always uses NewQoderHTTPClient and its strict pinned transport.
var QoderHTTPClientFactory = newQoderHTTPClient

// NewQoderHTTPClient uses exactly the pinned egress, including real direct
// transport for an empty proxy. It never honors environment proxy fallback,
// provider cookies, redirects, or global insecure TLS flags. The returned client
// can be copied to change timeout; its pooled transport must not be mutated.
func NewQoderHTTPClient(proxy string) (*http.Client, error) {
	return QoderHTTPClientFactory(proxy)
}

func newQoderHTTPClient(proxy string) (*http.Client, error) {
	var parsed *url.URL
	var err error
	if proxy != "" {
		parsed, err = common.ParseProxyURLStrict(proxy)
		if err != nil || parsed == nil {
			return nil, ErrAccountEgress
		}
	}
	policy := defaultHTTPTransportPolicy()
	policy.TLSInsecureSkipVerify = false
	var config *proxyURLConfig
	if parsed != nil {
		config = newProxyURLConfig(parsed)
	}
	client, err := qoderTransports.getOrCreate(proxy, config, policy, func() (*http.Client, error) {
		transport := newRelayHTTPTransport()
		transport.Proxy = nil
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		if parsed != nil {
			if err := configureProxyTransport(transport, parsed); err != nil {
				return nil, ErrAccountEgress
			}
		}
		applyHTTPTransportPolicy(transport, policy)
		return &http.Client{Transport: transport}, nil
	})
	if err != nil {
		return nil, ErrAccountEgress
	}
	copyClient := *client
	// Device polling against the regional gateway occasionally exceeds 20s end
	// to end; a premature deadline surfaced as a false upstream failure.
	copyClient.Timeout = 30 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copyClient, nil
}

func qoderHTTPClient(proxy string) (*qoderClient, error) {
	client, err := NewQoderHTTPClient(proxy)
	if err != nil {
		return nil, err
	}
	return &qoderClient{http: client}, nil
}

// request never returns provider bodies or URL-bearing transport errors to callers.
func (c *qoderClient) request(ctx context.Context, method, target string, headers http.Header, body []byte) (int, []byte, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, ErrAccountProtocol
	}
	req.Header = headers.Clone()
	req.GetBody = nil // Rotating refresh credentials must never be replayed.
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		// Operator-facing diagnostic only: host plus transport error text.
		common.SysLog("qoder transport error: host=" + nousHostOf(target) + " err=" + err.Error())
		return 0, nil, ErrAccountUpstream
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		common.SysLog("qoder read error: host=" + nousHostOf(target) + " err=" + err.Error())
		return 0, nil, ErrAccountUpstream
	}
	if len(raw) > 2<<20 {
		return 0, nil, ErrAccountProtocol
	}
	return resp.StatusCode, raw, nil
}

func qoderAuthHeaders(token string) http.Header {
	h := http.Header{"Accept": {"application/json"}, "User-Agent": {"QoderWork"}}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	return h
}

type qoderTokenResponse struct {
	Token        string          `json:"token"`
	DeviceToken  string          `json:"device_token"`
	RefreshToken string          `json:"refresh_token"`
	UserID       json.RawMessage `json:"user_id"`
	ExpiresIn    *int64          `json:"expires_in"`
	ExpiresAt    string          `json:"expires_at"`
}

func (r qoderTokenResponse) credential(realm, machineID string) (*QoderCredential, error) {
	token := r.Token
	if token == "" {
		token = r.DeviceToken
	}
	if !HeaderCredential(token, 16384) || (r.RefreshToken != "" && !HeaderCredential(r.RefreshToken, 16384)) {
		return nil, ErrAccountProtocol
	}
	var expires int64
	if r.ExpiresIn != nil {
		// The device endpoint returns milliseconds, not OAuth seconds.
		if *r.ExpiresIn <= 0 || *r.ExpiresIn > int64(10*365*24*time.Hour/time.Millisecond) {
			return nil, ErrAccountProtocol
		}
		expires = time.Now().Add(time.Duration(*r.ExpiresIn) * time.Millisecond).Unix()
	} else if r.ExpiresAt != "" {
		at, err := time.Parse(time.RFC3339, r.ExpiresAt)
		if err != nil || !at.After(time.Now()) {
			return nil, ErrAccountProtocol
		}
		expires = at.Unix()
	}
	// No invented default lifetime or subscription entitlement when omitted.
	return &QoderCredential{Realm: realm, Token: token, RefreshToken: r.RefreshToken, ExpiresAt: expires, MachineID: machineID}, nil
}

// PollQoderSignIn performs one poll; (nil, nil) means the browser grant is pending.
// The caller enforces owner/session binding, rate limiting, egress pinning and
// atomically consumes the encrypted ticket after success/cancel/expiry.
func PollQoderSignIn(ctx context.Context, proxy string, ticket *QoderTicket) (*QoderCredential, error) {
	client, err := qoderHTTPClient(proxy)
	if err != nil {
		return nil, err
	}
	return client.pollSignIn(ctx, ticket)
}

func (c *qoderClient) pollSignIn(ctx context.Context, ticket *QoderTicket) (*QoderCredential, error) {
	if ticket == nil || ticket.Until <= time.Now().Unix() {
		return nil, ErrDesktopLoginExpired
	}
	if ticket.Until > time.Now().Add(10*time.Minute).Unix()+5 || len(ticket.Verifier) < 43 || len(ticket.Verifier) > 128 || !HeaderCredential(ticket.Verifier, 128) || !HeaderCredential(ticket.Nonce, 64) || !HeaderCredential(ticket.MachineID, 128) {
		return nil, ErrAccountProtocol
	}
	cfg, err := qoderRealmConfig(ticket.Realm)
	if err != nil {
		return nil, err
	}
	q := url.Values{"nonce": {ticket.Nonce}, "verifier": {ticket.Verifier}, "challenge_method": {"S256"}}
	status, raw, err := c.request(ctx, http.MethodGet, cfg.api+"/api/v1/deviceToken/poll?"+q.Encode(), qoderAuthHeaders(""), nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusAccepted || status == http.StatusNotFound {
		return nil, nil
	}
	if status == http.StatusUnauthorized || status == http.StatusGone {
		return nil, ErrDesktopLoginExpired
	}
	if status != http.StatusOK {
		return nil, ErrAccountUpstream
	}
	var data qoderTokenResponse
	if common.Unmarshal(raw, &data) != nil {
		return nil, ErrAccountProtocol
	}
	cred, err := data.credential(ticket.Realm, ticket.MachineID)
	if err != nil {
		return nil, err
	}
	if err := c.loadIdentity(ctx, cfg, cred); err != nil {
		return nil, err
	}
	if time.Now().Unix() >= ticket.Until {
		return nil, ErrDesktopLoginExpired
	}
	return cred, nil
}

func (c *qoderClient) loadIdentity(ctx context.Context, cfg qoderRealm, cred *QoderCredential) error {
	status, raw, err := c.request(ctx, http.MethodGet, cfg.api+"/api/v1/userinfo", qoderAuthHeaders(cred.Token), nil)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return ErrDesktopLoginExpired
	}
	if status != http.StatusOK {
		return ErrAccountUpstream
	}
	var data struct {
		ID             json.RawMessage `json:"id"`
		Name           string          `json:"name"`
		UserType       string          `json:"user_type"`
		OrganizationID json.RawMessage `json:"organization_id"`
	}
	if common.Unmarshal(raw, &data) != nil {
		return ErrAccountProtocol
	}
	uid, err := qoderIdentityString(data.ID)
	if err != nil || !HeaderCredential(uid, 256) {
		return ErrAccountProtocol
	}
	if cred.UserID != "" && cred.UserID != uid {
		return ErrAccountProtocol
	}
	org, err := qoderIdentityString(data.OrganizationID)
	if err != nil || (org != "" && !HeaderCredential(org, 256)) || len(data.Name) > 512 || len(data.UserType) > 256 {
		return ErrAccountProtocol
	}
	cred.UserID, cred.Nickname, cred.UserType, cred.OrganizationID = uid, data.Name, data.UserType, org
	return nil
}

func qoderIdentityString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	var str string
	if common.Unmarshal(raw, &str) == nil {
		return str, nil
	}
	for _, b := range raw {
		if b < '0' || b > '9' {
			return "", ErrAccountProtocol
		}
	}
	if len(raw) > 256 {
		return "", ErrAccountProtocol
	}
	return string(raw), nil
}

// RenewQoderCredential refreshes only this owner device grant in the same realm.
// It returns a replacement value; the caller must CAS encrypted credentials
// outside provider I/O and reject an account whose egress identity changed.
func RenewQoderCredential(ctx context.Context, proxy string, cred *QoderCredential) (*QoderCredential, error) {
	client, err := qoderHTTPClient(proxy)
	if err != nil {
		return nil, err
	}
	return client.renewCredential(ctx, cred)
}

func (c *qoderClient) renewCredential(ctx context.Context, cred *QoderCredential) (*QoderCredential, error) {
	if cred == nil || !strings.HasPrefix(cred.RefreshToken, "drt-") || !HeaderCredential(cred.RefreshToken, 16384) || !HeaderCredential(cred.UserID, 256) {
		return nil, ErrDesktopLoginExpired
	}
	cfg, err := qoderRealmConfig(cred.Realm)
	if err != nil {
		return nil, err
	}
	body, err := common.Marshal(map[string]string{"refresh_token": cred.RefreshToken})
	if err != nil {
		return nil, ErrAccountProtocol
	}
	h := qoderAuthHeaders("")
	h.Set("Content-Type", "application/json")
	status, raw, err := c.request(ctx, http.MethodPost, cfg.api+"/api/v1/deviceToken/refresh", h, body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, ErrDesktopLoginExpired
	}
	if status != http.StatusOK {
		snippet := strings.Join(strings.Fields(string(raw)), " ")
		if len(snippet) > 320 {
			snippet = snippet[:320]
		}
		common.SysLog("qoder token refresh rejected: status=" + strconv.Itoa(status) + " body=" + snippet)
		return nil, ErrAccountUpstream
	}
	var data qoderTokenResponse
	if common.Unmarshal(raw, &data) != nil {
		return nil, ErrAccountProtocol
	}
	updated, err := data.credential(cred.Realm, cred.MachineID)
	if err != nil {
		return nil, err
	}
	if updated.RefreshToken == "" {
		updated.RefreshToken = cred.RefreshToken
	}
	updated.UserID, updated.Egress = cred.UserID, cred.Egress
	if err := c.loadIdentity(ctx, cfg, updated); err != nil {
		return nil, err
	}
	return updated, nil
}

// QoderRequestError sanitizes transport errors without losing cancellation identity.
func QoderRequestError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("qoder request: %w", ErrAccountUpstream)
}
