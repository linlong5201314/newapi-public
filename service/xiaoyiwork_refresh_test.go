package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type xiaoyiWorkRefreshTransport struct{ calls int }

func (transport *xiaoyiWorkRefreshTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls++
	if request.URL.Path == "/user-credential/v1/xiaoyiwork/refresh-token" {
		assert.Equal(nil, transport.calls, 1)
		assert.Contains(nil, transport.readerBody(request), "fixture-refresh-token")
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"token":"fixture-new-access-token"}`))}, nil
	}
	assert.Equal(nil, transport.calls, 2)
	assert.Equal(nil, "Bearer fixture-new-access-token", request.Header.Get("Authorization"))
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"credentialResult":{"businessCredential":"fixture-renewed-credential","validTime":4102444800}}`))}, nil
}

func (transport *xiaoyiWorkRefreshTransport) readerBody(request *http.Request) string {
	body, _ := io.ReadAll(request.Body)
	return string(body)
}

func TestXiaoyiWorkRefreshRenewsCredentialWithoutMagpie(t *testing.T) {
	transport := &xiaoyiWorkRefreshTransport{}
	restore := SetXiaoyiWorkTransportForTest(transport)
	defer restore()
	session := &XiaoyiWorkOAuthSession{AccessToken: "fixture-old-access", RefreshToken: "fixture-refresh-token", UID: "fixture-uid", DeviceID: "fixture-device", Credential: "fixture-old-credential", ValidTimeSec: time.Now().Add(-time.Hour).Unix()}
	require.NoError(t, session.Fresh(context.Background(), ""))
	assert.Equal(t, "fixture-renewed-credential", session.Credential)
	assert.Equal(t, "fixture-new-access-token", session.AccessToken)
	assert.Equal(t, 2, transport.calls)
	assert.Equal(t, session.Credential, session.Headers("fixture")["businessCredential"][0])
}
