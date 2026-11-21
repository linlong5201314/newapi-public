package service

// COSY wire codec adapted from shuishuipingan/qoder2api-hub qoder_sign.py,
// qoder_proxy.py, qoder_catalog.py at 25d05944cdc9b36cb69b162d9cba0d8e8989f4db.
// Copyright (c) 2026. MIT permission notice reproduced below.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" // Provider wire checksum, not a password hash or security primitive.
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
)

const QoderInferencePath = "/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"
const qoderModelsPath = "/algo/api/v2/model/list?Encode=1"
const qoderVersion = "1.1.75"
const qoderAlphabet = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"
const qoderPublicKey = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----`

// EncodeQoderBody encodes native JSON using Qoder's alphabet and 3-segment order.
func EncodeQoderBody(plain []byte) string {
	std := base64.StdEncoding.EncodeToString(plain)
	a := len(std) / 3
	rearranged := std[len(std)-a:] + std[a:len(std)-a] + std[:a]
	out := []byte(rearranged)
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	for i, b := range out {
		if b == '=' {
			out[i] = '$'
		} else {
			out[i] = qoderAlphabet[strings.IndexByte(alphabet, b)]
		}
	}
	return string(out)
}

// QoderSignedHeaders signs the exact encoded wire body and exact verified path.
// The server's legacy RSA1024/PKCS1v15, AES-CBC(key=IV), and MD5 wire formats
// cannot be replaced unilaterally. TLS remains mandatory. This intentionally
// omits synthetic machineToken, machineType, UMID, hostname, or client IP proof.
func QoderSignedHeaders(cred *QoderCredential, body, target, modelKey string) (http.Header, error) {
	if cred == nil || !HeaderCredential(cred.UserID, 256) || !HeaderCredential(cred.Token, 16384) || !HeaderCredential(cred.MachineID, 128) || (cred.RefreshToken != "" && !HeaderCredential(cred.RefreshToken, 16384)) || (modelKey != "" && !HeaderCredential(modelKey, 256)) {
		return nil, ErrAccountProtocol
	}
	if cred.ExpiresAt > 0 && cred.ExpiresAt <= time.Now().Unix() {
		return nil, ErrDesktopLoginExpired
	}
	base, err := QoderGateway(cred.Realm)
	if err != nil {
		return nil, err
	}
	if target != base+qoderModelsPath && target != base+QoderInferencePath {
		return nil, ErrAccountProtocol
	}
	// Retain the upstream ASCII-hex 16-byte key format for compatibility, but
	// generate it from crypto/rand. No identity is derived from host/user files.
	keyRaw := make([]byte, 8)
	if _, err := rand.Read(keyRaw); err != nil {
		return nil, ErrAccountProtocol
	}
	key := []byte(hex.EncodeToString(keyRaw))
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrAccountProtocol
	}
	// Struct field order is sorted exactly as upstream json_sorted_compact.
	identity := struct {
		AID              string `json:"aid"`
		Name             string `json:"name"`
		OrganizationID   string `json:"organization_id"`
		OrganizationName string `json:"organization_name"`
		RefreshToken     string `json:"refresh_token"`
		Token            string `json:"security_oauth_token"`
		UID              string `json:"uid"`
		UserType         string `json:"user_type"`
		YXUID            string `json:"yx_uid"`
	}{AID: cred.UserID, Name: cred.Nickname, OrganizationID: cred.OrganizationID, RefreshToken: cred.RefreshToken, Token: cred.Token, UID: cred.UserID, UserType: cred.UserType}
	plain, err := common.Marshal(identity)
	if err != nil {
		return nil, ErrAccountProtocol
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := make([]byte, len(plain)+pad)
	copy(padded, plain)
	for i := len(plain); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key).CryptBlocks(encrypted, padded)
	der, _ := pem.Decode([]byte(qoderPublicKey))
	if der == nil {
		return nil, ErrAccountProtocol
	}
	public, err := x509.ParsePKIXPublicKey(der.Bytes)
	if err != nil {
		return nil, ErrAccountProtocol
	}
	rsaPublic, ok := public.(*rsa.PublicKey)
	if !ok {
		return nil, ErrAccountProtocol
	}
	wrapped, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPublic, key)
	if err != nil {
		return nil, ErrAccountProtocol
	}
	cosyKey := base64.StdEncoding.EncodeToString(wrapped)
	payload := struct {
		CosyVersion string `json:"cosyVersion"`
		IDEVersion  string `json:"ideVersion"`
		Info        string `json:"info"`
		RequestID   string `json:"requestId"`
		Version     string `json:"version"`
	}{qoderVersion, "", base64.StdEncoding.EncodeToString(encrypted), uuid.NewString(), "v1"}
	payloadJSON, err := common.Marshal(payload)
	if err != nil {
		return nil, ErrAccountProtocol
	}
	payload64 := base64.StdEncoding.EncodeToString(payloadJSON)
	parsed, _ := url.Parse(target)
	path := strings.TrimPrefix(parsed.Path, "/algo")
	date := strconv.FormatInt(time.Now().Unix(), 10)
	sum := md5.Sum([]byte(payload64 + "\n" + cosyKey + "\n" + date + "\n" + body + "\n" + path))
	h := http.Header{}
	h.Set("Authorization", "Bearer COSY."+payload64+"."+hex.EncodeToString(sum[:]))
	h.Set("Cosy-Key", cosyKey)
	h.Set("Cosy-Date", date)
	h.Set("Cosy-User", cred.UserID)
	h.Set("Cosy-Machineid", cred.MachineID)
	h.Set("Cosy-Version", qoderVersion)
	h.Set("Login-Version", "v2")
	h.Set("Cosy-Clienttype", "5")
	h.Set("Cosy-Business-Product", "cli")
	h.Set("Cosy-Business-Type", "agent")
	h.Set("Cosy-Scene", "assistant")
	h.Set("Cosy-Data-Policy", "agree")
	h.Set("Content-Type", "application/json")
	h.Set("Accept-Encoding", "identity")
	h.Set("User-Agent", "NewAPI-Qoder-Bridge/1.0")
	if modelKey != "" {
		h.Set("X-Model-Key", modelKey)
		h.Set("X-Model-Source", "system")
		h.Set("Accept", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
	} else {
		h.Set("Accept", "application/json")
	}
	return h, nil
}

// QoderModel is a real authenticated catalog entry, never a static entitlement.
type QoderModel struct {
	Key            string `json:"key"`
	DisplayName    string `json:"display_name"`
	Enable         *bool  `json:"enable"`
	IsVL           bool   `json:"is_vl"`
	IsReasoning    bool   `json:"is_reasoning"`
	MaxInputTokens int64  `json:"max_input_tokens"`
}

// FetchQoderModels returns only enabled chat/app keys from the authenticated
// native catalog. Failure/empty catalog does not fall back to advertised models.
func FetchQoderModels(ctx context.Context, proxy string, cred *QoderCredential) ([]string, error) {
	models, err := FetchQoderModelCatalog(ctx, proxy, cred)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.Key)
	}
	return out, nil
}

func FetchQoderModelCatalog(ctx context.Context, proxy string, cred *QoderCredential) ([]QoderModel, error) {
	client, err := qoderHTTPClient(proxy)
	if err != nil {
		return nil, err
	}
	return client.fetchModels(ctx, cred)
}

func (c *qoderClient) fetchModels(ctx context.Context, cred *QoderCredential) ([]QoderModel, error) {
	if cred == nil {
		return nil, ErrAccountProtocol
	}
	base, err := QoderGateway(cred.Realm)
	if err != nil {
		return nil, err
	}
	body := EncodeQoderBody([]byte("{}"))
	headers, err := QoderSignedHeaders(cred, body, base+qoderModelsPath, "")
	if err != nil {
		return nil, err
	}
	status, raw, err := c.request(ctx, http.MethodGet, base+qoderModelsPath, headers, []byte(body))
	if err != nil {
		common.SysLog("qoder catalog transport error: " + err.Error())
		return nil, err
	}
	if status == http.StatusUnauthorized {
		return nil, ErrDesktopLoginExpired
	}
	if status != http.StatusOK {
		// Operator-facing diagnostic only; never surfaced to the API caller.
		snippet := strings.Join(strings.Fields(string(raw)), " ")
		if len(snippet) > 320 {
			snippet = snippet[:320]
		}
		common.SysLog("qoder catalog rejected: status=" + strconv.Itoa(status) + " body=" + snippet)
		return nil, ErrAccountUpstream
	}
	var payload struct {
		Chat []QoderModel `json:"chat"`
		App  []QoderModel `json:"app"`
	}
	if common.Unmarshal(raw, &payload) != nil || len(payload.Chat)+len(payload.App) > 1024 {
		return nil, ErrAccountProtocol
	}
	out := make([]QoderModel, 0, len(payload.Chat)+len(payload.App))
	seen := map[string]bool{}
	for _, scene := range [][]QoderModel{payload.Chat, payload.App} {
		for _, m := range scene {
			if !HeaderCredential(m.Key, 256) || strings.ContainsAny(m.Key, ",/\\") || seen[m.Key] {
				continue
			}
			seen[m.Key] = true // Disabled chat entry must not be overridden by app metadata.
			if m.Enable == nil || !*m.Enable {
				continue
			}
			if len(m.DisplayName) > 512 || m.MaxInputTokens < 0 {
				return nil, ErrAccountProtocol
			}
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, ErrAccountProtocol
	}
	return out, nil
}
