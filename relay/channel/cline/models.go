package cline

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// FetchUpstreamModels 拉取 Cline 的模型清单。
//
// 两条路合并：
//   - GET {base}/models —— 全量模型（需要 workos 令牌）
//   - GET {base}/ai/cline/recommended-models —— 插件推荐/免费通道（可能免鉴权）
//
// 任何一路成功即返回；两路都失败时返回错误，由调用方决定是否回退内置 ModelList。
func FetchUpstreamModels(base, accessToken string) ([]string, error) {
	base = strings.TrimSuffix(strings.TrimSpace(base), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	client := &http.Client{Timeout: 30 * time.Second}

	seen := make(map[string]struct{})
	var models []string
	add := func(list []string) {
		for _, m := range list {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			models = append(models, m)
		}
	}

	var errs []string
	if list, err := fetchModelList(client, base+"/models", accessToken); err != nil {
		errs = append(errs, err.Error())
	} else {
		add(list)
	}
	if list, err := fetchRecommendedModels(client, base+"/ai/cline/recommended-models", accessToken); err != nil {
		errs = append(errs, err.Error())
	} else {
		add(list)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("cline: 未能获取模型清单（%s）", strings.Join(errs, "; "))
	}
	return models, nil
}

func fetchModelList(client *http.Client, url, accessToken string) ([]string, error) {
	body, status, err := clineGet(client, url, accessToken)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET %s -> HTTP %d: %s", url, status, truncate(body, 200))
	}
	// 兼容两种形状：{"data":[{"id":...}]} 与 {"models":[...]} / 裸数组
	var wrap struct {
		Data   []map[string]any `json:"data"`
		Models []any            `json:"models"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err == nil {
		var ids []string
		for _, item := range wrap.Data {
			if id, ok := item["id"].(string); ok {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			return ids, nil
		}
		for _, item := range wrap.Models {
			if s, ok := item.(string); ok {
				ids = append(ids, s)
			}
		}
		if len(ids) > 0 {
			return ids, nil
		}
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err == nil {
		var ids []string
		for _, item := range raw {
			if id, ok := item["id"].(string); ok {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			return ids, nil
		}
	}
	return nil, fmt.Errorf("GET %s 返回结构无法识别: %s", url, truncate(body, 160))
}

func fetchRecommendedModels(client *http.Client, url, accessToken string) ([]string, error) {
	body, status, err := clineGet(client, url, accessToken)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET %s -> HTTP %d", url, status)
	}
	// 常见形状：{"cline":[{"id":...}], "free":[{"id":...}]} 或 {"data":[...]}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &generic); err != nil {
		return nil, err
	}
	var ids []string
	for _, raw := range generic {
		var items []map[string]any
		if err := json.Unmarshal(raw, &items); err != nil {
			continue
		}
		for _, item := range items {
			if id, ok := item["id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("GET %s 没有模型条目", url)
	}
	return ids, nil
}

func clineGet(client *http.Client, url, accessToken string) (string, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	for name, value := range ClientFingerprint {
		req.Header.Set(name, value)
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+AuthPrefix+strings.TrimSpace(strings.TrimPrefix(accessToken, AuthPrefix)))
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if len(buf) > 1<<20 {
				break
			}
		}
		if readErr != nil {
			break
		}
	}
	return string(buf), resp.StatusCode, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
