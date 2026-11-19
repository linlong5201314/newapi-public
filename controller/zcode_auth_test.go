package controller

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/zcode"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestZCodeAccountsDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db, identity, request, client := zcodeControllerFixture(t, dialect)
			pending, denyPlan, blockPoll := true, false, false
			pollCount, balanceCount, relayCount := 0, 0, 0
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			jwt := "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(time.Hour).Unix()))) + ".fixture-signature"
			client.Transport = workBuddyTestTransport(func(r *http.Request) (*http.Response, error) {
				assert.Equal(t, "zcode.z.ai", r.URL.Host)
				body := "{}"
				switch r.URL.Path {
				case "/api/v1/oauth/cli/init":
					body = fmt.Sprintf(`{"code":0,"data":{"flow_id":"zcode-flow","authorize_url":"https://bigmodel.cn/login?appId=zcode&state=fixture","expires_at":%d,"poll_interval_sec":2}}`, time.Now().Add(5*time.Minute).Unix())
				case "/api/v1/oauth/cli/poll/zcode-flow":
					pollCount++
					if blockPoll {
						entered <- struct{}{}
						<-release
					}
					body = `{"code":0,"data":{"status":"pending"}}`
					if !pending {
						body = fmt.Sprintf(`{"code":0,"data":{"status":"ready","token":%q,"user":{"user_id":"own-zcode-user"},"bigmodel":{"access_token":"unused-business-fixture"}}}`, jwt)
					}
				case "/api/v1/zcode-plan/billing/balance":
					balanceCount++
					assert.Equal(t, "Bearer "+jwt, r.Header.Get("Authorization"))
					body = `{"code":0,"data":{"plans":[{"user_plan_id":"own-plan","plan_id":"start-plan","status":"active"}],"balances":[{"user_plan_id":"own-plan","capabilities":["model:GLM-5.3-Flash","model:foreign"],"remaining_units":10}]}}`
					if denyPlan {
						body = `{"code":0,"data":{"plans":[],"balances":[]}}`
					}
				case "/api/v1/zcode-plan/anthropic/v1/messages":
					relayCount++
					assert.Equal(t, "Bearer "+jwt, r.Header.Get("Authorization"))
					assert.Equal(t, jwt, r.Header.Get("X-Api-Key"))
					assert.Empty(t, r.Header.Get("Cookie"))
					body = `{"id":"msg_fixture","type":"message","role":"assistant","model":"GLM-5.3-Flash","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`
				default:
					t.Fatalf("unexpected path %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			decode := func(res *httptest.ResponseRecorder) map[string]any {
				require.Equal(t, 200, res.Code, res.Body.String())
				var b map[string]any
				require.NoError(t, common.Unmarshal(res.Body.Bytes(), &b))
				require.Equal(t, true, b["success"], res.Body.String())
				return b["data"].(map[string]any)
			}
			start := func() string {
				res := request(SelfZCodeOAuthStart, "POST", `{"realm":"bigmodel","proxyPolicy":{"mode":"direct"}}`, "https://dashboard.test", identity)
				data := decode(res)
				assert.NotContains(t, res.Body.String(), "pollToken")
				return data["sessionId"].(string)
			}
			reset := func(token string) {
				require.NoError(t, model.WithWorkBuddyAuthFlow(token, identity, func(_ *gorm.DB, s *model.WorkBuddyAuthState) error { s.NextPollAt = 0; return nil }))
			}
			poll := func(token string) *httptest.ResponseRecorder {
				return request(SelfZCodeOAuthPoll, "POST", fmt.Sprintf(`{"sessionId":%q}`, token), "https://dashboard.test", identity)
			}
			cancel := func(token string) *httptest.ResponseRecorder {
				return request(SelfZCodeOAuthCancel, "POST", fmt.Sprintf(`{"sessionId":%q}`, token), "https://dashboard.test", identity)
			}
			token := start()
			assert.Equal(t, 429, poll(token).Code)
			reset(token)
			assert.Equal(t, false, decode(poll(token))["done"])
			reset(token)
			pending = false
			data := decode(poll(token))
			assert.Equal(t, true, data["done"])
			channelJSON := data["channel"].(map[string]any)
			id := int(channelJSON["id"].(float64))
			assert.Equal(t, "", channelJSON["key"])
			assert.NotContains(t, fmt.Sprint(data), jwt)
			stored, err := model.GetChannelById(id, true)
			require.NoError(t, err)
			assert.Equal(t, identity.UserID, stored.OwnerId)
			assert.Equal(t, 0, stored.Visibility)
			assert.Equal(t, "GLM-5.3-Flash", stored.Models)
			assert.True(t, common.IsEncryptedSecret(stored.Key))

			refreshed := request(func(c *gin.Context) { _, handled := SelfFetchAccountModels(c, stored); assert.True(t, handled) }, "GET", "", "https://dashboard.test", identity)
			require.Equal(t, 200, refreshed.Code, refreshed.Body.String())
			assert.Contains(t, refreshed.Body.String(), "GLM-5.3-Flash")
			assert.NotContains(t, refreshed.Body.String(), "foreign")
			other := identity
			other.UserID++
			foreignRefresh := request(func(c *gin.Context) { _, handled := SelfFetchAccountModels(c, stored); assert.True(t, handled) }, "GET", "", "https://dashboard.test", other)
			assert.NotEqual(t, 200, foreignRefresh.Code)
			for _, patch := range []string{fmt.Sprintf(`{"id":%d,"key":"replacement"}`, id), fmt.Sprintf(`{"id":%d,"TYPE":1}`, id), fmt.Sprintf(`{"id":%d,"models":"foreign-model"}`, id)} {
				rejected := request(UpdateSelfChannel, "PUT", patch, "https://dashboard.test", identity)
				assert.NotContains(t, rejected.Body.String(), `"success":true`, patch)
			}
			count := pollCount
			decode(poll(token))
			assert.Equal(t, count, pollCount)
			assert.Equal(t, 2, balanceCount)
			foreign := identity
			foreign.SessionID = uuid.NewString()
			assert.NotEqual(t, 200, request(SelfZCodeOAuthPoll, "POST", fmt.Sprintf(`{"sessionId":%q}`, token), "https://dashboard.test", foreign).Code)
			assert.Equal(t, 403, request(SelfZCodeOAuthStart, "POST", `{"realm":"bigmodel"}`, "https://foreign.invalid", identity).Code)
			plain, err := common.DecryptSecret(stored.Key)
			require.NoError(t, err)
			assert.NotContains(t, plain, "unused-business-fixture")
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: id, ChannelType: constant.ChannelTypeZCode, ApiKey: plain, ChannelBaseUrl: stored.GetBaseURL(), UpstreamModelName: "GLM-5.3-Flash", ChannelSetting: stored.GetSetting()}, RelayMode: relayconstant.RelayModeChatCompletions}
			rr := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rr)
			c.Request = httptest.NewRequest("POST", "https://dashboard.test/v1/chat/completions", nil)
			adaptor := &zcode.Adaptor{}
			result, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"GLM-5.3-Flash","messages":[]}`))
			require.NoError(t, err)
			res := result.(*http.Response)
			require.NoError(t, res.Body.Close())
			assert.Equal(t, 1, relayCount)

			info.HeadersOverride = map[string]any{"Authorization": "Bearer injected"}
			_, err = adaptor.DoRequest(c, info, strings.NewReader(`{}`))
			require.Error(t, err)
			assert.Equal(t, 1, relayCount)
			info.HeadersOverride = nil
			info.ChannelBaseUrl = "https://attacker.invalid"
			_, err = adaptor.DoRequest(c, info, strings.NewReader(`{}`))
			require.Error(t, err)
			assert.Equal(t, 1, relayCount)
			info.ChannelBaseUrl = stored.GetBaseURL()
			info.UpstreamModelName = "foreign"
			_, err = adaptor.DoRequest(c, info, strings.NewReader(`{}`))
			require.Error(t, err)
			info.UpstreamModelName = "GLM-5.3-Flash"
			cancelToken := start()
			assert.Equal(t, 200, cancel(cancelToken).Code)
			assert.Equal(t, 409, poll(cancelToken).Code)
			denyPlan = true
			missing := start()
			reset(missing)
			assert.Equal(t, 403, poll(missing).Code)
			denyPlan = false
			// Network I/O cannot hold a database writer lock. Cancellation wins over a late response.
			blocked := start()
			reset(blocked)
			blockPoll = true
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- poll(blocked) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("provider poll not entered")
			}
			cancelled := make(chan *httptest.ResponseRecorder, 1)
			go func() { cancelled <- cancel(blocked) }()
			select {
			case response := <-cancelled:
				require.Equal(t, 200, response.Code, response.Body.String())
			case <-time.After(3 * time.Second):
				close(release)
				t.Fatal("provider HTTP held database writer lock")
			}
			close(release)
			select {
			case response := <-done:
				assert.Equal(t, 409, response.Code, response.Body.String())
			case <-time.After(3 * time.Second):
				t.Fatal("late poll did not finish")
			}
			var total int64
			require.NoError(t, db.Model(&model.Channel{}).Where("owner_id = ?", identity.UserID).Count(&total).Error)
			assert.Equal(t, int64(1), total)
		})
	}
}
