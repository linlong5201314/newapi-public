package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// LoginDS2Account implements CJackHwang/ds2api's DeepSeek account login contract.
// DeepSeek does not offer an OAuth consent/redirect flow in that integration.
// No caller-controlled endpoint, invented cookie, or browser script is involved.
func LoginDS2Account(ctx context.Context, account, password, proxy string) (string, error) {
	account = strings.TrimSpace(account)
	if account == "" || password == "" || len(account) > 320 || len(password) > 1024 {
		return "", errors.New("Account and password are required")
	}
	payload := map[string]any{"password": password, "device_id": "deepseek_to_api", "os": "android"}
	if strings.Contains(account, "@") {
		payload["email"] = account
	} else {
		mobile := strings.TrimPrefix(strings.TrimSpace(account), "+")
		if len(mobile) == 13 && strings.HasPrefix(mobile, "86") {
			mobile = mobile[2:]
		}
		for _, digit := range mobile {
			if digit < '0' || digit > '9' {
				return "", errors.New("Invalid account format")
			}
		}
		if len(mobile) < 6 {
			return "", errors.New("Invalid account format")
		}
		payload["mobile"], payload["area_code"] = mobile, nil
	}
	raw, err := common.Marshal(payload)
	if err != nil {
		return "", errors.New("Unable to start upstream login")
	}
	client, err := GetHttpClientWithProxySettings(proxy, dto.ChannelSettings{})
	if err != nil {
		return "", errors.New("Unable to connect to the configured proxy")
	}
	boundedClient := *client
	boundedClient.Timeout = 20 * time.Second
	boundedClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chat.deepseek.com/api/v0/users/login", bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("Unable to start upstream login")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "DeepSeek/2.0.4 Android/35")
	request.Header.Set("x-client-platform", "android")
	request.Header.Set("x-client-version", "2.0.4")
	request.Header.Set("x-client-locale", "zh_CN")
	response, err := boundedClient.Do(request)
	if err != nil {
		return "", errors.New("Upstream login is unavailable. Please try again later")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("Upstream login failed or requires additional verification. Complete verification on the official website and retry")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil || len(body) > 256<<10 {
		return "", errors.New("Invalid upstream login response")
	}
	var result struct {
		Code *int `json:"code"`
		Data struct {
			Code *int `json:"biz_code"`
			Data struct {
				User struct {
					Token string `json:"token"`
				} `json:"user"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if common.Unmarshal(body, &result) != nil || result.Code == nil || *result.Code != 0 || result.Data.Code == nil || *result.Data.Code != 0 {
		return "", errors.New("Upstream login failed or requires additional verification. Complete verification on the official website and retry")
	}
	token := strings.TrimSpace(result.Data.Data.User.Token)
	if token == "" || len(token) > 8192 || strings.ContainsAny(token, " \r\n\t") {
		return "", errors.New("Invalid upstream login response")
	}
	return token, nil
}
