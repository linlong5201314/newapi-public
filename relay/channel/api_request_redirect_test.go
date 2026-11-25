package channel

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoRequestRouteTimeoutStopsAtResponseHeaders(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	headerSent := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		close(headerSent)
		<-time.After(120 * time.Millisecond)
		_, _ = io.WriteString(w, "second\n")
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	baseCtx, clientCancel := context.WithCancel(context.Background())
	defer clientCancel()
	ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil).WithContext(baseCtx)
	attemptCtx, release := service.RouteRuleAttemptContextForTest(ctx, baseCtx, 40*time.Millisecond)
	defer release()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	resp, err := doRequest(ctx, req, info)
	require.NoError(t, err)
	defer resp.Body.Close()
	<-headerSent
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "first-byte timeout must not truncate the response body")
	assert.Equal(t, "first\nsecond\n", string(body))
	assert.NoError(t, baseCtx.Err())
}

func TestDoRequestRouteTimeoutStillCancelsSlowHeaders(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-time.After(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)
	attemptCtx, release := service.RouteRuleAttemptContextForTest(ctx, ctx.Request.Context(), 20*time.Millisecond)
	defer release()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	resp, err := doRequest(ctx, req, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Error(t, attemptCtx.Err())
}

func TestDoRequestRouteTimeoutStillPropagatesClientDisconnect(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		<-time.After(time.Second)
	}))
	defer server.Close()
	baseCtx, clientCancel := context.WithCancel(context.Background())
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil).WithContext(baseCtx)
	attemptCtx, release := service.RouteRuleAttemptContextForTest(ctx, baseCtx, 40*time.Millisecond)
	defer release()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	resp, err := doRequest(ctx, req, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	require.NoError(t, err)
	defer resp.Body.Close()
	clientCancel()
	_, err = io.ReadAll(resp.Body)
	require.Error(t, err)
}

func TestDoRequestReturnsUpstreamRedirectWithoutFollowing(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	sharedClient := service.GetHttpClient()
	require.NotNil(t, sharedClient)
	require.NotNil(t, sharedClient.CheckRedirect)
	originalRedirectPolicy := reflect.ValueOf(sharedClient.CheckRedirect).Pointer()

	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer target.Close()

	const responseBody = "redirect response"
	tests := []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	}

	for _, statusCode := range tests {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			targetRequests.Store(0)
			var sourceRequests atomic.Int32
			type sourceResult struct {
				body []byte
				err  error
			}
			sourceResultCh := make(chan sourceResult, 1)
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sourceRequests.Add(1)
				body, err := io.ReadAll(r.Body)
				sourceResultCh <- sourceResult{body: body, err: err}
				w.Header().Set("Location", target.URL+"/redirect-target")
				w.WriteHeader(statusCode)
				_, _ = io.WriteString(w, responseBody)
			}))
			defer source.Close()

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

			req, err := http.NewRequest(http.MethodPost, source.URL, bytes.NewReader([]byte("request body")))
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

			resp, err := doRequest(ctx, req, info)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			gotSource := <-sourceResultCh
			require.NoError(t, gotSource.err)

			assert.Equal(t, statusCode, resp.StatusCode)
			assert.Equal(t, target.URL+"/redirect-target", resp.Header.Get("Location"))
			assert.Equal(t, responseBody, string(body))
			assert.Equal(t, []byte("request body"), gotSource.body)
			assert.EqualValues(t, 1, sourceRequests.Load())
			assert.Zero(t, targetRequests.Load())
		})
	}

	assert.Equal(t, originalRedirectPolicy, reflect.ValueOf(sharedClient.CheckRedirect).Pointer(), "the cached client must not be mutated")
}
