package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestXiaoyiWorkStreamNormalizesFramesAndRejectsInterruption(t *testing.T) {
	t.Run("valid frames receive metadata and DONE", func(t *testing.T) {
		body := &xiaoyiWorkStreamBody{reader: io.NopCloser(strings.NewReader("id: 7\ndata: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")), model: "fixture-model"}
		content, err := io.ReadAll(body)
		require.NoError(t, err)
		assert.Contains(t, string(content), `"object":"chat.completion.chunk"`)
		assert.Contains(t, string(content), `"model":"fixture-model"`)
		assert.Contains(t, string(content), `"created":`)
		assert.NotContains(t, string(content), "id: 7")
		assert.True(t, strings.HasSuffix(string(content), "data: [DONE]\n\n"))
	})

	t.Run("truncated JSON is not silently completed", func(t *testing.T) {
		body := &xiaoyiWorkStreamBody{reader: io.NopCloser(strings.NewReader("data: {\"choices\":")), model: "fixture-model"}
		_, err := io.ReadAll(body)
		require.ErrorContains(t, err, "incomplete stream frame")
	})

	t.Run("network interruption is not a clean EOF", func(t *testing.T) {
		body := &xiaoyiWorkStreamBody{reader: &failingReadCloser{err: errors.New("connection reset")}, model: "fixture-model"}
		_, err := io.ReadAll(body)
		require.ErrorContains(t, err, "connection reset")
	})

	t.Run("client cancellation stops the stream", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		body := &xiaoyiWorkStreamBody{ctx: ctx, reader: io.NopCloser(strings.NewReader("data: {}\n\n")), model: "fixture-model"}
		_, err := io.ReadAll(body)
		require.ErrorIs(t, err, context.Canceled)
	})
}

type failingReadCloser struct{ err error }

func (reader *failingReadCloser) Read([]byte) (int, error) { return 0, reader.err }
func (reader *failingReadCloser) Close() error             { return nil }

func TestXiaoyiWorkAggregatePreservesToolArgumentsAndUsage(t *testing.T) {
	stream := "data: {\"id\":\"fixture\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call-one\",\"type\":\"function\",\"function\":{\"name\":\"workspace\",\"arguments\":\"{\\\"path\\\":\\\"src\\\",\\\"line\\\":7,\"}}]}},{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"write\",\"arguments\":\"\\\"text\\\":\\\"ok\\\"}\"}}]}}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3,\"total_tokens\":8}}\n\n"
	response, err := XiaoyiWorkAggregate(strings.NewReader(stream), "fixture-model")
	require.NoError(t, err)
	require.Len(t, response.Choices, 1)
	assert.Equal(t, "tool_calls", response.Choices[0].FinishReason)
	var calls []struct {
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	require.NoError(t, common.Unmarshal(response.Choices[0].Message.ToolCalls, &calls))
	require.Len(t, calls, 1)
	assert.Equal(t, "workspacewrite", calls[0].Function.Name)
	var arguments map[string]any
	require.NoError(t, common.Unmarshal([]byte(calls[0].Function.Arguments), &arguments))
	assert.Equal(t, "src", arguments["path"])
	assert.Equal(t, float64(7), arguments["line"])
	assert.Equal(t, "ok", arguments["text"])
	assert.Equal(t, 8, response.Usage.TotalTokens)
}

func TestXiaoyiWorkProviderErrorsContainNoUpstreamBody(t *testing.T) {
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error_description":"private-account-detail","access_token":"do-not-leak"}`))}, nil
	})
	restore := SetXiaoyiWorkTransportForTest(transport)
	defer restore()
	client, err := xiaoyiWorkHTTPClient("")
	require.NoError(t, err)
	var output map[string]any
	err = xiaoyiWorkRequest(context.Background(), client, http.MethodPost, xiaoyiWorkOAuthToken, nil, nil, &output)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OAuth token exchange")
	assert.Contains(t, err.Error(), "400")
	assert.NotContains(t, err.Error(), "private-account-detail")
	assert.NotContains(t, err.Error(), "do-not-leak")
}

func TestXiaoyiWorkRelayPersistsOnlyActualRefresh(t *testing.T) {
	previousSecret, previousConfigured := common.CryptoSecret, common.CryptoSecretConfigured
	common.CryptoSecret, common.CryptoSecretConfigured = "xiaoyi-relay-test-key", true
	t.Cleanup(func() { common.CryptoSecret, common.CryptoSecretConfigured = previousSecret, previousConfigured })
	// No token refresh or credential issuance should happen for a valid session.
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/celia/v1/sse-api/chat/completions" {
			return nil, errors.New("unnecessary credential refresh")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))}, nil
	})
	restore := SetXiaoyiWorkTransportForTest(transport)
	defer restore()
	raw, err := common.Marshal(&XiaoyiWorkOAuthSession{Credential: "fixture-credential", UID: "fixture-uid", DeviceID: "fixture-device", ValidTimeSec: time.Now().Add(48 * time.Hour).Unix()})
	require.NoError(t, err)
	request, err := http.NewRequest(http.MethodPost, XiaoyiWorkSSEEndpoint, strings.NewReader(`{"model":"fixture-model","messages":[]}`))
	require.NoError(t, err)
	stored, err := common.EncryptSecret(string(raw))
	require.NoError(t, err)
	require.True(t, common.IsEncryptedSecret(stored))
	response, apiErr := DoXiaoyiWorkRelay(context.Background(), 0, stored, request, "fixture-model", false)
	if apiErr != nil {
		t.Logf("valid-session relay failure: %s", apiErr.Error())
	}
	require.Nil(t, apiErr)
	defer response.Body.Close()
	assert.Equal(t, http.StatusOK, response.StatusCode)
}

func TestXiaoyiWorkRelayUsesLatestPersistedCredential(t *testing.T) {
	previousDB, previousSecret, previousConfigured := model.DB, common.CryptoSecret, common.CryptoSecretConfigured
	common.CryptoSecret, common.CryptoSecretConfigured = "xiaoyi-relay-latest-key", true
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = database
	require.NoError(t, database.AutoMigrate(&model.Channel{}))
	t.Cleanup(func() {
		model.DB = previousDB
		common.CryptoSecret, common.CryptoSecretConfigured = previousSecret, previousConfigured
	})
	encrypt := func(credential string) string {
		raw, err := common.Marshal(&XiaoyiWorkOAuthSession{Credential: credential, UID: "fixture-uid", DeviceID: "fixture-device", ValidTimeSec: time.Now().Add(48 * time.Hour).Unix()})
		require.NoError(t, err)
		stored, err := common.EncryptSecret(string(raw))
		require.NoError(t, err)
		return stored
	}
	channel := &model.Channel{OwnerId: 701, Type: constant.ChannelTypeXiaoyiWork, Key: encrypt("latest-persisted-credential"), Models: "fixture-model", Status: common.ChannelStatusEnabled}
	require.NoError(t, database.Create(channel).Error)
	restore := SetXiaoyiWorkTransportForTest(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, []string{"latest-persisted-credential"}, request.Header["businessCredential"])
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))}, nil
	}))
	defer restore()
	request, err := http.NewRequest(http.MethodPost, XiaoyiWorkSSEEndpoint, strings.NewReader(`{"model":"fixture-model","messages":[]}`))
	require.NoError(t, err)
	response, apiErr := DoXiaoyiWorkRelay(context.Background(), channel.Id, encrypt("stale-cache-credential"), request, "fixture-model", false)
	require.Nil(t, apiErr)
	response.Body.Close()
	assert.Equal(t, http.StatusOK, response.StatusCode)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
