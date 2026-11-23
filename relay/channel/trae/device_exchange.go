package trae

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const epBoundExchange = "/trae/api/v3/oauth/ExchangeToken"

type boundTokenResult struct {
	Result struct {
		Token               string `json:"Token"`
		RefreshToken        string `json:"RefreshToken"`
		TokenExpireAt       int64  `json:"TokenExpireAt"`
		TokenExpireDuration int64  `json:"TokenExpireDuration"`
	} `json:"Result"`
}

func deviceProof(path, clientID, refresh, privateKey string, now time.Time) (map[string]any, error) {
	block, _ := pem.Decode([]byte(privateKey))
	if block == nil {
		return nil, fmt.Errorf("invalid device private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("invalid device private key")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve.Params().Name != "P-256" {
		return nil, fmt.Errorf("unsupported device private key")
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, err
	}
	nonce := fmt.Sprintf("%x", nonceBytes)
	timestamp := now.Unix()
	text := strings.Join([]string{"POST", path, clientID, refresh, fmt.Sprint(timestamp), nonce}, "\n")
	digest := sha256.Sum256([]byte(text))
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		return nil, err
	}
	return map[string]any{"Signature": base64.StdEncoding.EncodeToString(signature), "Timestamp": timestamp, "Nonce": nonce}, nil
}

// boundExchange renews a desktop/Magpie JWT with the device's P-256 proof.
// Unlike the legacy cloudide exchange, this binds the token to the imported
// desktop device and does not unnecessarily invalidate that session.
func (c *Client) boundExchange() (*Cred, error) {
	if c.cred.DevicePublicKey == "" || c.cred.DevicePrivateKey == "" {
		return nil, fmt.Errorf("device keypair is required for bound token exchange")
	}
	candidates := []struct{ id, platform, version string }{
		{"en1oxy7wnw8j9n", "SOLO_PC", "0.1.64"},
		{IDEClientID, "IDE_PC", "3.3.99"},
	}
	var last error
	for _, candidate := range candidates {
		proof, err := deviceProof(epBoundExchange, candidate.id, c.cred.RefreshToken, c.cred.DevicePrivateKey, time.Now())
		if err != nil {
			return nil, err
		}
		body := map[string]any{
			"ClientID": candidate.id, "ClientSecret": "", "RefreshToken": c.cred.RefreshToken,
			"DeviceInfo": map[string]any{
				"DeviceID": c.cred.DeviceID, "MachineID": c.cred.MachineID,
				"PlatformCode": candidate.platform, "DeviceType": "PC", "DeviceName": "", "DeviceModel": "",
				"ClientVersion": candidate.version, "DevicePublicKey": c.cred.DevicePublicKey,
				"DeviceBrand": "ASUS TUF Gaming A15 FA507RM_FA507RM", "DeviceCPU": "AMD", "OSInfo": "Windows", "OSVersion": "10.0.22631",
			},
			"DeviceProof": proof, "IDEVersion": candidate.version,
		}
		raw, _ := common.Marshal(body)
		req, err := http.NewRequest(http.MethodPost, c.oauthBase()+epBoundExchange, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		OAuthHeaders(req)
		if c.cred.AccessToken != "" {
			req.Header.Set("Authorization", "Cloud-IDE-JWT "+c.cred.AccessToken)
		}
		data, err := c.doJSON(req)
		if err != nil {
			last = err
			continue
		}
		var response boundTokenResult
		if common.Unmarshal(data, &response) != nil || response.Result.Token == "" {
			last = fmt.Errorf("bound exchange returned no token")
			continue
		}
		next := *c.cred
		next.AccessToken = response.Result.Token
		if response.Result.RefreshToken != "" {
			next.RefreshToken = response.Result.RefreshToken
		}
		if response.Result.TokenExpireAt > 0 {
			next.ExpiresAt = NormalizeExpire(response.Result.TokenExpireAt)
		} else if response.Result.TokenExpireDuration > 0 {
			next.ExpiresAt = time.Now().Add(time.Duration(response.Result.TokenExpireDuration) * time.Second).Unix()
		}
		if next.ExpiresAt <= 0 {
			next.ExpiresAt = CallbackTokenExpiry(next.AccessToken)
		}
		return &next, nil
	}
	return nil, last
}
