package controller

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/trae"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTraeBridgeSessionBoundConsent(t *testing.T) {
	uid := setupChannelCreateTest(t)
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.UserSession{}, &model.AuthFlow{}, &model.Option{}))
	session := selfContractDS2Session(t, uid)
	identity := model.AuthSessionIdentity{UserID: uid, SessionID: session.SID, UserAuthVersion: session.UserAuthVersion, SessionVersion: session.Version}
	oldAddress, oldSecret, oldConfigured := system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured
	oldEnabled, oldLimit := operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser
	system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = "https://gateway.example", "trae-bridge-regression-encryption-key", true
	operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = true, 20
	t.Cleanup(func() {
		system_setting.ServerAddress, common.CryptoSecret, common.CryptoSecretConfigured = oldAddress, oldSecret, oldConfigured
		operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = oldEnabled, oldLimit
	})
	serviceCalls := 0
	var providerEntered, providerRelease chan struct{}
	providerUID, failProvider := "trae-owner", false
	setSelfContractTransport(t, func(request *http.Request) (*http.Response, error) {
		serviceCalls++
		assert.Equal(t, "api.trae.cn", request.URL.Host)
		assert.Equal(t, trae.EpUserInfo, request.URL.Path, "fresh callback must not rotate its token pair")
		assert.True(t, strings.HasPrefix(request.Header.Get("Authorization"), "Cloud-IDE-JWT private-trae-access"))
		if providerEntered != nil {
			close(providerEntered)
			<-providerRelease
		}
		if failProvider {
			return selfContractResponse(503, `{"private":"not-for-response"}`)
		}
		return selfContractResponse(200, fmt.Sprintf(`{"Result":{"UserID":%q,"ScreenName":"Verified account"}}`, providerUID))
	})
	request := func(id model.AuthSessionIdentity, method, path, body, origin string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", id.UserID)
		c.Set("session_id", id.SessionID)
		c.Set("auth_version", id.UserAuthVersion)
		c.Set("session_version", id.SessionVersion)
		c.Set("role", common.RoleCommonUser)
		c.Request = httptest.NewRequest(method, "https://gateway.example"+path, strings.NewReader(body))
		// Production gateway rewrites Host; forwarded headers are not authority.
		c.Request.Host = "internal-node.up.railway.app"
		c.Request.Header.Set("X-Forwarded-Host", "attacker.example")
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("X-Account-Action", "authorize")
		if origin != "" {
			c.Request.Header.Set("Origin", origin)
		}
		handler(c)
		return recorder
	}
	decode := func(recorder *httptest.ResponseRecorder) map[string]any {
		var response map[string]any
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		return response
	}
	start := func() string {
		response := request(identity, http.MethodPost, traeBridgePath+"/start", `{"realm":"cn","confirmRotation":true,"proxy":"direct"}`, system_setting.ServerAddress, SelfTraeBridgeStart)
		require.Equal(t, 200, response.Code, response.Body.String())
		data := decode(response)["data"].(map[string]any)
		assert.Equal(t, true, data["requiresConsent"])
		assert.Equal(t, true, data["requiresLocalHelper"])
		assert.NotContains(t, data, "authUrl")
		return data["sessionId"].(string)
	}
	download := func(flow string) map[string]any {
		response := request(identity, http.MethodPost, traeBridgePath+"/helper", fmt.Sprintf(`{"sessionId":%q}`, flow), system_setting.ServerAddress, SelfTraeBridgeHelper)
		require.Equal(t, 200, response.Code, response.Body.String())
		assert.Contains(t, response.Header().Get("Content-Disposition"), "attachment")
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		match := regexp.MustCompile(`CONFIG_BASE64 = "([^"]+)"`).FindStringSubmatch(response.Body.String())
		require.Len(t, match, 2)
		raw, err := base64.StdEncoding.DecodeString(match[1])
		require.NoError(t, err)
		var config map[string]any
		require.NoError(t, common.Unmarshal(raw, &config))
		assert.Equal(t, "https://gateway.example"+traeBridgePath+"/upload", config["upload_url"])
		assert.True(t, common.IsEncryptedSecret(config["capability"].(string)))
		assert.NotContains(t, response.Body.String(), flow)
		assert.NotContains(t, response.Body.String(), session.SID)
		return config
	}
	callback := func(uid string) string {
		jwt, err := common.Marshal(map[string]any{"Token": "private-trae-access", "RefreshToken": "private-trae-refresh", "TokenExpireAt": time.Now().Add(time.Hour).Unix(), "ClientID": trae.IDEClientID})
		require.NoError(t, err)
		account, err := common.Marshal(map[string]any{"UserID": uid, "ScreenName": "Untrusted name", "AuthHost": trae.DomesticAuthHost})
		require.NoError(t, err)
		return "http://127.0.0.1:32123/authorize?" + url.Values{"userJwt": {string(jwt)}, "userInfo": {string(account)}}.Encode()
	}
	upload := func(config map[string]any, callbackURL, browserOrigin string) *httptest.ResponseRecorder {
		body, err := common.Marshal(map[string]any{"callbackUrl": callbackURL, "machineId": strings.Repeat("a", 64), "deviceId": "e5d45474-96bf-4ae0-9364-8d70dcfe5e34"})
		require.NoError(t, err)
		response := request(model.AuthSessionIdentity{}, http.MethodPost, traeBridgePath+"/upload", string(body), browserOrigin, func(c *gin.Context) {
			c.Request.Header.Set("Authorization", "Bearer "+config["capability"].(string))
			SelfTraeBridgeUpload(c)
		})
		assert.NotContains(t, response.Body.String(), "private-trae")
		assert.NotContains(t, response.Body.String(), config["capability"])
		return response
	}
	consent := func(flow, token, uid string) *httptest.ResponseRecorder {
		body, err := common.Marshal(map[string]any{"sessionId": flow, "consentToken": token, "uid": uid})
		require.NoError(t, err)
		return request(identity, http.MethodPost, traeBridgePath+"/consent", string(body), system_setting.ServerAddress, SelfTraeBridgeConsent)
	}
	countChannels := func(want int64) {
		var count int64
		require.NoError(t, db.Model(&model.Channel{}).Where("owner_id = ?", uid).Count(&count).Error)
		assert.Equal(t, want, count)
	}
	for _, body := range []string{`{"realm":"global","confirmRotation":true}`, `{"realm":"cn"}`} {
		response := request(identity, http.MethodPost, traeBridgePath+"/start", body, system_setting.ServerAddress, SelfTraeBridgeStart)
		assert.Equal(t, 400, response.Code)
	}
	flow := start()
	firstConfig := download(flow)
	config := download(flow)
	assert.Equal(t, 400, upload(firstConfig, callback(providerUID), "").Code, "redownload revokes previous upload capability")
	assert.Equal(t, 400, upload(config, callback(providerUID), "https://attacker.example").Code)
	assert.Equal(t, 400, upload(config, strings.Replace(callback(providerUID), "127.0.0.1", "attacker.example", 1), "").Code)
	assert.Equal(t, 202, upload(config, callback(providerUID), "").Code)
	assert.Equal(t, 409, upload(config, callback(providerUID), "").Code)
	countChannels(0)
	response := request(identity, http.MethodGet, traeBridgePath+"/poll?sessionId="+flow, "", system_setting.ServerAddress, SelfTraeBridgePoll)
	require.Equal(t, 200, response.Code, response.Body.String())
	data := decode(response)["data"].(map[string]any)
	assert.Equal(t, "awaiting_consent", data["status"])
	assert.Equal(t, "Verified account", data["account"].(map[string]any)["nickname"])
	assert.NotContains(t, response.Body.String(), "private-trae")
	consentToken := data["consentToken"].(string)
	countChannels(0)
	_, err := PollAccountAuthorization(context.Background(), identity, flow)
	assert.Error(t, err, "the generic poll entry point must not bypass bridge consent")
	countChannels(0)
	assert.Equal(t, 400, consent(flow, strings.Repeat("x", 43), providerUID).Code)
	assert.Equal(t, 400, consent(flow, consentToken, "attacker").Code)
	countChannels(0)
	response = consent(flow, consentToken, providerUID)
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"done":true`)
	assert.NotContains(t, response.Body.String(), "private-trae")
	countChannels(1)
	assert.Equal(t, 1, serviceCalls, "consent never re-redeems callback tokens")
	var channel model.Channel
	require.NoError(t, db.Where("owner_id = ?", uid).First(&channel).Error)
	assert.Equal(t, constant.ChannelTypeTrae, channel.Type)
	assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
	assert.True(t, common.IsEncryptedSecret(channel.Key))
	assert.NotContains(t, channel.Key, "private-trae")
	assert.NotEqual(t, 200, consent(flow, consentToken, providerUID).Code, "consent cannot be replayed")
	countChannels(1)
	response = request(identity, http.MethodGet, traeBridgePath+"/poll?sessionId="+flow, "", system_setting.ServerAddress, SelfTraeBridgePoll)
	assert.Equal(t, 200, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"done":true`)
	countChannels(1)
	for _, scenario := range []string{"owner", "sid", "origin", "expiry", "cancel", "revoked", "auth_version", "provider_failure", "account_injection"} {
		t.Run(scenario, func(t *testing.T) {
			flow := start()
			config := download(flow)
			body := fmt.Sprintf(`{"sessionId":%q}`, flow)
			switch scenario {
			case "owner":
				other := identity
				other.UserID++
				assert.NotEqual(t, 200, request(other, http.MethodPost, traeBridgePath+"/helper", body, system_setting.ServerAddress, SelfTraeBridgeHelper).Code)
			case "sid":
				otherSession := *session
				otherSession.SID = uuid.NewString()
				require.NoError(t, db.Create(&otherSession).Error)
				other := identity
				other.SessionID = otherSession.SID
				assert.Equal(t, 400, request(other, http.MethodPost, traeBridgePath+"/helper", body, system_setting.ServerAddress, SelfTraeBridgeHelper).Code)
			case "origin":
				assert.Equal(t, 403, request(identity, http.MethodPost, traeBridgePath+"/helper", body, "https://attacker.example", SelfTraeBridgeHelper).Code)
			case "expiry":
				require.NoError(t, db.Model(&model.AuthFlow{}).Where("purpose = ? AND consumed_at IS NULL", model.WorkBuddyAuthPurpose).Update("expires_at", time.Now().Add(-time.Second)).Error)
				assert.Equal(t, 410, upload(config, callback(providerUID), "").Code)
			case "cancel":
				assert.Equal(t, 200, request(identity, http.MethodPost, traeBridgePath+"/cancel", body, system_setting.ServerAddress, SelfTraeBridgeCancel).Code)
				assert.Equal(t, 409, upload(config, callback(providerUID), "").Code)
			case "revoked", "auth_version":
				if scenario == "revoked" {
					require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", session.SID).Update("status", "revoked").Error)
				} else {
					require.NoError(t, db.Model(&model.User{}).Where("id = ?", uid).Update("auth_version", 2).Error)
				}
				assert.Equal(t, 401, upload(config, callback(providerUID), "").Code)
				require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", session.SID).Update("status", model.UserSessionStatusActive).Error)
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", uid).Update("auth_version", 1).Error)
			case "provider_failure", "account_injection":
				// Use a fresh pair so the one-shot replay assertion is independently exercised.
				callbackURL := strings.ReplaceAll(strings.ReplaceAll(callback(providerUID), "private-trae-access", "private-trae-access-"+scenario), "private-trae-refresh", "private-trae-refresh-"+scenario)
				if scenario == "provider_failure" {
					failProvider = true
				} else {
					providerUID = "different-provider-account"
				}
				assert.Equal(t, 202, upload(config, callbackURL, "").Code)
				response := request(identity, http.MethodGet, traeBridgePath+"/poll?sessionId="+flow, "", system_setting.ServerAddress, SelfTraeBridgePoll)
				assert.Equal(t, 502, response.Code, response.Body.String())
				assert.NotContains(t, response.Body.String(), "private-trae")
				assert.NotContains(t, response.Body.String(), "not-for-response")
				failProvider, providerUID = false, "trae-owner"
			}
			countChannels(1)
			// Explicit cleanup prevents active-flow quota from masking later cases.
			require.NoError(t, db.Where("purpose = ? AND consumed_at IS NULL", model.WorkBuddyAuthPurpose).Delete(&model.AuthFlow{}).Error)
		})
	}
	t.Run("blocked upstream permits writes and cancellation", func(t *testing.T) {
		other := model.User{Username: "trae-bridge-other-" + uuid.NewString(), AffCode: uuid.NewString(), Status: common.UserStatusEnabled}
		require.NoError(t, db.Create(&other).Error)
		flow := start()
		config := download(flow)
		callbackURL := strings.ReplaceAll(strings.ReplaceAll(callback(providerUID), "private-trae-access", "private-trae-access-blocked"), "private-trae-refresh", "private-trae-refresh-blocked")
		require.Equal(t, 202, upload(config, callbackURL, "").Code)
		providerEntered, providerRelease = make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(providerRelease) }) }
		defer release()
		pollDone := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			pollDone <- request(identity, http.MethodGet, traeBridgePath+"/poll?sessionId="+flow, "", system_setting.ServerAddress, SelfTraeBridgePoll)
		}()
		select {
		case <-providerEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("provider request did not start")
		}
		// A second poll cannot issue another provider request or wait on it.
		concurrentDone := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			concurrentDone <- request(identity, http.MethodGet, traeBridgePath+"/poll?sessionId="+flow, "", system_setting.ServerAddress, SelfTraeBridgePoll)
		}()
		select {
		case result := <-concurrentDone:
			assert.Equal(t, 200, result.Code, result.Body.String())
			assert.Contains(t, result.Body.String(), `"done":false`)
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent poll held behind provider I/O")
		}
		writeDone := make(chan error, 1)
		go func() {
			writeDone <- db.Model(&model.User{}).Where("id = ?", other.Id).Update("display_name", "write-completed-during-provider-request").Error
		}()
		select {
		case err := <-writeDone:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("provider HTTP blocked another user's database write")
		}
		cancelDone := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			cancelDone <- request(identity, http.MethodPost, traeBridgePath+"/cancel", fmt.Sprintf(`{"sessionId":%q}`, flow), system_setting.ServerAddress, SelfTraeBridgeCancel)
		}()
		select {
		case result := <-cancelDone:
			require.Equal(t, 200, result.Code, result.Body.String())
		case <-time.After(5 * time.Second):
			t.Fatal("provider HTTP blocked cancellation")
		}
		release()
		select {
		case result := <-pollDone:
			assert.Equal(t, 409, result.Code, result.Body.String())
			assert.NotContains(t, result.Body.String(), "consentToken")
		case <-time.After(5 * time.Second):
			t.Fatal("released provider response did not complete")
		}
		providerEntered, providerRelease = nil, nil
		countChannels(1)
	})
}

func TestTraeBridgePythonLoopbackBoundary(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("Python interpreter is required for the distributed helper regression")
	}
	helper, err := filepath.Abs("assets/trae_login_helper.py")
	require.NoError(t, err)
	// This imports the source without running main(): no browser or workstation
	// login is launched. Only a private, loopback-bound test socket is opened.
	script := `
import base64, http.client, importlib.util, json, sys, threading, time, urllib.parse
spec = importlib.util.spec_from_file_location("trae_bridge_test", sys.argv[1])
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
config = {"origin":"https://gateway.example","upload_url":"https://gateway.example"+m.UPLOAD_PATH,"capability":"a"*64,"expires_at":int(time.time())+120}
assert m.load_config(base64.b64encode(json.dumps(config).encode()))["origin"] == config["origin"]
for field, value in [("origin","http://gateway.example"),("upload_url","https://attacker.example/collect"),("expires_at",int(time.time())-1)]:
    bad = dict(config); bad[field] = value
    try: m.load_config(base64.b64encode(json.dumps(bad).encode())); raise AssertionError("unsafe config accepted")
    except ValueError: pass
server = m.LoginServer(time.time()+60)
query = urllib.parse.urlencode({"userJwt":json.dumps({"Token":"private-token","RefreshToken":"private-refresh","ClientID":m.CLIENT_ID}),"userInfo":json.dumps({"UserID":"owner","ScreenName":"Account"})})
path = "/authorize?"+query
url = urllib.parse.urlsplit(m.authorization_url(server,"a"*64,"device"))
params = urllib.parse.parse_qs(url.query)
assert params["auth_callback_url"] == ["http://"+server.authority+"/authorize"]
assert "state" not in params["auth_callback_url"][0] and url.netloc == "www.trae.cn"
def request(path, headers, method="GET"):
    task = threading.Thread(target=server.handle_request); task.start()
    conn = http.client.HTTPConnection("127.0.0.1", server.server_port, timeout=3)
    conn.request(method,path,headers=headers)
    response = conn.getresponse(); status = response.status; body = response.read(); conn.close(); task.join(3)
    assert not task.is_alive()
    assert b"private-token" not in body and b"private-refresh" not in body
    return status
assert request(path,{"Host":"attacker.example","Origin":m.OFFICIAL_ORIGIN}) == 403
assert request(path,{"Origin":"https://attacker.example"}) == 403
assert request(path,{}) == 403
assert request(path,{"Origin":"null"}) == 403
assert request(path+"&userJwt=duplicate",{"Origin":m.OFFICIAL_ORIGIN}) == 400
assert request(path,{"Origin":m.OFFICIAL_ORIGIN}) == 200
assert server.account == ("owner","Account")
assert request(path,{"Origin":m.OFFICIAL_ORIGIN}) == 403
server.server_close()
assert m.safe_label("\x1b[31m\u202eattacker") != "\x1b[31m\u202eattacker"
print("helper loopback success, exact callback, origin/host/injection/expiry/replay: PASS")
`
	command := exec.Command(python, "-I", "-c", script, helper)
	command.Dir = t.TempDir()
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	assert.Contains(t, string(output), "PASS")
	t.Log(strings.TrimSpace(string(output)))
}
