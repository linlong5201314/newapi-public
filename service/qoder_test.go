package service

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type qoderRoundTrip func(*http.Request) (*http.Response, error)

func (f qoderRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func qoderTestResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestQoderDeviceGrantContracts(t *testing.T) {
	for _, realm := range []string{"cn", "global"} {
		t.Run(realm, func(t *testing.T) {
			ticket, raw, err := StartQoderSignIn(context.Background(), "", realm)
			require.NoError(t, err)
			require.NotNil(t, ticket)
			parsed, err := url.Parse(raw)
			require.NoError(t, err)
			q := parsed.Query()
			sum := sha256.Sum256([]byte(ticket.Verifier))
			assert.Equal(t, base64.RawURLEncoding.EncodeToString(sum[:]), q.Get("challenge"))
			assert.Equal(t, "S256", q.Get("challenge_method"))
			assert.Equal(t, ticket.Nonce, q.Get("nonce"))
			assert.Equal(t, ticket.MachineID, q.Get("machine_id"))
			assert.Empty(t, q.Get("verifier"))
			assert.Empty(t, q.Get("code"))
			if realm == "cn" {
				assert.Equal(t, "qoder.com.cn", parsed.Host)
				assert.Equal(t, "qoder-work-cn://", q.Get("redirect_uri"))
			} else {
				assert.Equal(t, "qoder.com", parsed.Host)
				assert.False(t, q.Has("redirect_uri"))
			}
			client := qoderClient{http: &http.Client{Transport: qoderRoundTrip(func(r *http.Request) (*http.Response, error) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/v1/deviceToken/poll", r.URL.Path)
				assert.Equal(t, ticket.Verifier, r.URL.Query().Get("verifier"))
				assert.Empty(t, r.Header.Get("Authorization"))
				return qoderTestResponse(http.StatusAccepted, ""), nil
			})}}
			got, err := client.pollSignIn(context.Background(), ticket)
			require.NoError(t, err)
			assert.Nil(t, got)
			ticket.Until = time.Now().Add(-time.Second).Unix()
			_, err = client.pollSignIn(context.Background(), ticket)
			assert.ErrorIs(t, err, ErrDesktopLoginExpired)
		})
	}
	_, _, err := StartQoderSignIn(context.Background(), "", "intl")
	assert.ErrorIs(t, err, ErrAccountProtocol)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = StartQoderSignIn(ctx, "", "cn")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestQoderPollIdentityAndFailure(t *testing.T) {
	ticket, _, err := StartQoderSignIn(context.Background(), "", "global")
	require.NoError(t, err)
	for _, tc := range []struct {
		name           string
		status         int
		body, identity string
		want           error
		pending        bool
	}{
		{name: "success", status: 200, body: `{"token":"dt-owner","refresh_token":"drt-owner","expires_in":3600000}`, identity: `{"id":9001,"name":"owner","user_type":"personal_standard"}`},
		{name: "pending404", status: 404, pending: true},
		{name: "malformed-is-not-pending", status: 200, body: `{"token":`, want: ErrAccountProtocol},
		{name: "missing-token", status: 200, body: `{}`, want: ErrAccountProtocol},
		{name: "header-injection", status: 200, body: `{"token":"bad\r\nsecret"}`, want: ErrAccountProtocol},
		{name: "identity-required", status: 200, body: `{"token":"dt-owner"}`, identity: `{"name":"unknown"}`, want: ErrAccountProtocol},
		{name: "no-redirect", status: 302, body: "token-secret", want: ErrAccountUpstream},
		{name: "upstream-body-not-disclosed", status: 500, body: "token-secret", want: ErrAccountUpstream},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := qoderClient{http: &http.Client{Transport: qoderRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return qoderTestResponse(tc.status, tc.body), nil
				}
				assert.Equal(t, "/api/v1/userinfo", r.URL.Path)
				assert.Equal(t, "Bearer dt-owner", r.Header.Get("Authorization"))
				return qoderTestResponse(200, tc.identity), nil
			})}}
			got, err := client.pollSignIn(context.Background(), ticket)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				assert.NotContains(t, err.Error(), "secret")
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			if tc.pending {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, "9001", got.UserID)
			assert.Equal(t, "personal_standard", got.UserType)
			assert.InDelta(t, time.Now().Add(time.Hour).Unix(), got.ExpiresAt, 2)
		})
	}
}

func TestQoderRenewalIdentityAndCancellation(t *testing.T) {
	cred := &QoderCredential{Realm: "cn", Token: "dt-old", RefreshToken: "drt-owner", UserID: "42", MachineID: "installation-id", Egress: AccountEgress{Key: "owner-key", Proxy: "direct"}}
	for _, id := range []string{"42", "43"} {
		t.Run(id, func(t *testing.T) {
			client := qoderClient{http: &http.Client{Transport: qoderRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/v1/deviceToken/refresh" {
					assert.Equal(t, http.MethodPost, r.Method)
					assert.Nil(t, r.GetBody, "rotating refresh credentials must never be replayed")
					assert.Empty(t, r.Header.Get("Authorization"))
					raw, e := io.ReadAll(r.Body)
					require.NoError(t, e)
					assert.JSONEq(t, `{"refresh_token":"drt-owner"}`, string(raw))
					return qoderTestResponse(200, `{"token":"dt-new"}`), nil
				}
				return qoderTestResponse(200, `{"id":"`+id+`","user_type":"personal_standard"}`), nil
			})}}
			next, err := client.renewCredential(context.Background(), cred)
			if id != "42" {
				assert.ErrorIs(t, err, ErrAccountProtocol)
				assert.Nil(t, next)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, next)
			assert.Equal(t, "dt-new", next.Token)
			assert.Equal(t, cred.Egress, next.Egress)
			assert.Equal(t, cred.RefreshToken, next.RefreshToken)
			assert.Zero(t, next.ExpiresAt)
			assert.Equal(t, "dt-old", cred.Token)
		})
	}
	client := qoderClient{http: &http.Client{Transport: qoderRoundTrip(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := client.request(ctx, http.MethodGet, "https://openapi.qoder.sh", nil, nil)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestQoderNativeModelsAndSignature(t *testing.T) {
	cred := &QoderCredential{Realm: "global", UserID: "42", Token: "dt-owner", MachineID: "installation-id", UserType: "personal_standard"}
	body := EncodeQoderBody([]byte("{}"))
	base, err := QoderGateway(cred.Realm)
	require.NoError(t, err)
	headers, err := QoderSignedHeaders(cred, body, base+qoderModelsPath, "")
	require.NoError(t, err)
	parts := strings.Split(strings.TrimPrefix(headers.Get("Authorization"), "Bearer COSY."), ".")
	require.Len(t, parts, 2)
	sum := md5.Sum([]byte(parts[0] + "\n" + headers.Get("Cosy-Key") + "\n" + headers.Get("Cosy-Date") + "\n" + body + "\n/api/v2/model/list"))
	assert.Equal(t, hex.EncodeToString(sum[:]), parts[1])
	for _, name := range []string{"Cosy-Machinetoken", "Cosy-Machinetype", "Cosy-Clientip", "Cookie", "X-Umid"} {
		assert.Empty(t, headers.Get(name))
	}
	encoded, err := base64.StdEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), cred.Token)
	_, err = QoderSignedHeaders(cred, body, "https://evil.invalid/algo/api/v2/model/list?Encode=1", "")
	assert.ErrorIs(t, err, ErrAccountProtocol)
	client := qoderClient{http: &http.Client{Transport: qoderRoundTrip(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "api3.qoder.sh", r.URL.Host)
		assert.Equal(t, "1", r.URL.Query().Get("Encode"))
		raw, e := io.ReadAll(r.Body)
		require.NoError(t, e)
		assert.Equal(t, body, string(raw))
		assert.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer COSY."))
		return qoderTestResponse(200, `{"chat":[{"key":"allowed","enable":true},{"key":"disabled","enable":false},{"key":"unknown"}],"app":[{"key":"disabled","enable":true},{"key":"app-only","enable":true}],"inline":[{"key":"not-chat","enable":true}]}`), nil
	})}}
	models, err := client.fetchModels(context.Background(), cred)
	require.NoError(t, err)
	require.Len(t, models, 2)
	assert.Equal(t, "allowed", models[0].Key)
	assert.Equal(t, "app-only", models[1].Key)
	var token qoderTokenResponse
	require.NoError(t, common.Unmarshal([]byte(`{"token":"dt-owner","expires_in":-1}`), &token))
	_, err = token.credential("global", "installation")
	assert.ErrorIs(t, err, ErrAccountProtocol)
	assert.False(t, errors.Is(ErrAccountProtocol, ErrAccountUpstream))
}

func TestQoderStrictTransportAndCancellation(t *testing.T) {
	client, err := NewQoderHTTPClient("")
	require.NoError(t, err)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Nil(t, transport.Proxy, "direct egress must not inherit proxy environment")
	require.NotNil(t, transport.TLSClientConfig)
	assert.False(t, transport.TLSClientConfig.InsecureSkipVerify)
	assert.GreaterOrEqual(t, transport.TLSClientConfig.MinVersion, uint16(tls.VersionTLS12))
	assert.Nil(t, client.Jar)
	assert.ErrorIs(t, client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
	_, err = NewQoderHTTPClient("http://proxy.invalid:1/path")
	assert.ErrorIs(t, err, ErrAccountEgress)

	entered := make(chan struct{})
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(finished)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := (&qoderClient{http: client}).request(ctx, http.MethodGet, server.URL, nil, nil)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach local server")
	}
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Qoder request did not terminate on cancellation")
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("server request did not observe cancellation")
	}
	assert.Equal(t, "$kwm", EncodeQoderBody([]byte("{}")))
}
