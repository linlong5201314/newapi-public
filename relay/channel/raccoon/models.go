package raccoon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// AccessTokenOf 从渠道密钥里取出 access token。
//
// 允许三种写法（都在真实凭据里出现过）：
//  1. 裸 JWT：eyJhbGciOi...
//  2. JSON：{"access":"eyJ...","refresh":"..."}（浏览器授权回调换回来的原始结果）
//  3. 带 Bearer 前缀：Bearer eyJ...
func AccessTokenOf(key string) string {
	key = strings.TrimSpace(key)
	key = strings.TrimSpace(strings.TrimPrefix(key, "Bearer "))
	if key == "" {
		return ""
	}
	if strings.HasPrefix(key, "{") {
		var v struct {
			Access       string `json:"access"`
			AccessToken  string `json:"access_token"`
			AccessToken2 string `json:"accessToken"`
		}
		if err := json.Unmarshal([]byte(key), &v); err == nil {
			for _, cand := range []string{v.Access, v.AccessToken, v.AccessToken2} {
				if s := strings.TrimSpace(cand); s != "" {
					return strings.TrimSpace(strings.TrimPrefix(s, "Bearer "))
				}
			}
		}
	}
	return key
}

// FetchUpstreamModels 拉取小浣熊的模型目录：GET {base}/model_catalog。
//
// 返回的形状在不同版本里出现过三种（data 数组 / data.items / models 数组），
// 这里统一用「递归找带 id 的对象」兜住，避免上游一改字段就整条链挂掉。
func FetchUpstreamModels(base, key string) ([]string, error) {
	base = strings.TrimSuffix(strings.TrimSpace(base), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	token := AccessTokenOf(key)
	if token == "" {
		return nil, fmt.Errorf("raccoon: 渠道密钥为空，请先完成浏览器授权")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, base+"/model_catalog", nil)
	if err != nil {
		return nil, err
	}
	for name, value := range BrowserHeaders {
		req.Header.Set(name, value)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 8192)
	for {
		n, readErr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if len(buf) > 4<<20 {
				break
			}
		}
		if readErr != nil {
			break
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s/model_catalog -> HTTP %d: %s", base, resp.StatusCode, truncate(string(buf), 200))
	}
	var root any
	if err := json.Unmarshal(buf, &root); err != nil {
		return nil, fmt.Errorf("raccoon: 模型目录不是合法 JSON: %s", truncate(string(buf), 160))
	}
	ids := collectModelIDs(root, nil)
	if len(ids) == 0 {
		return nil, fmt.Errorf("raccoon: 模型目录里没有模型条目: %s", truncate(string(buf), 160))
	}
	return ids, nil
}

// collectModelIDs 递归找所有形如 {"id":"..."} 的条目，按出现顺序去重。
func collectModelIDs(node any, acc []string) []string {
	seen := make(map[string]struct{}, len(acc))
	for _, s := range acc {
		seen[s] = struct{}{}
	}
	var walk func(any)
	walk = func(n any) {
		switch v := n.(type) {
		case map[string]any:
			if id, ok := v["id"].(string); ok {
				id = strings.TrimSpace(id)
				if id != "" {
					if _, dup := seen[id]; !dup {
						seen[id] = struct{}{}
						acc = append(acc, id)
					}
				}
			}
			if model, ok := v["model"].(string); ok {
				model = strings.TrimSpace(model)
				if model != "" {
					if _, dup := seen[model]; !dup {
						seen[model] = struct{}{}
						acc = append(acc, model)
					}
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(node)
	return acc
}

// UserInfo 用 access token 读账号信息（/api/web/auth/v1/user_info 的 data.phone）。
// 失败时返回空串，不影响建渠道（只是卡片上少一个手机号）。
func UserInfo(key string) string {
	token := AccessTokenOf(key)
	if token == "" {
		return ""
	}
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://xiaohuanxiong.com/api/web/auth/v1/user_info", nil)
	if err != nil {
		return ""
	}
	for name, value := range BrowserHeaders {
		req.Header.Set(name, value)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if readErr != nil {
			break
		}
	}
	var payload struct {
		Data struct {
			Phone string `json:"phone"`
			Name  string `json:"name"`
		} `json:"data"`
	}
	if json.Unmarshal(buf, &payload) != nil {
		return ""
	}
	if payload.Data.Phone != "" {
		return payload.Data.Phone
	}
	return payload.Data.Name
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
