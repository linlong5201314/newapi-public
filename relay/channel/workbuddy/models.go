// models.go WorkBuddy 动态模型目录：/v3/config（IDE UA 完整能力版）+
// 企业端点（/console/enterprises/personal/models）两路并发探测、按 id 合并。
// 移植自 _research/workbuddy2api-panel-main internal/upstream/client.go（裁剪：
// promotions/global 探测目录/modelsdev 不搬）。
package workbuddy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ModelInfo 动态模型信息。
type ModelInfo struct {
	ID            string
	Name          string
	ContextWindow int64
	MaxTokens     int64
}

// dynModelEntry 上游模型目录单条模型解析形态（两域同构）。
type dynModelEntry struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ModelID         string `json:"modelId"`
	Model           string `json:"model"`
	MaxInputTokens  int64  `json:"maxInputTokens"`
	MaxOutputTokens int64  `json:"maxOutputTokens"`
	Disabled        bool   `json:"disabled"`
	Tags            []string `json:"tags"`
}

func (m dynModelEntry) modelInfo() ModelInfo {
	return ModelInfo{
		ID:            m.ID,
		Name:          m.Name,
		ContextWindow: m.MaxInputTokens,
		MaxTokens:     m.MaxOutputTokens,
	}
}

// nonChatModel 判定是否非对话模型（nes-/completion-/codewise- 前缀、tiny 输出、生成类标签）。
func nonChatModel(id string, maxOutputTokens int64, tags []string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, p := range [...]string{"nes-", "completion-", "codewise-"} {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	if maxOutputTokens > 0 && maxOutputTokens <= 256 {
		return true
	}
	for _, t := range tags {
		switch t {
		case "text-to-image", "image-to-image", "text-to-video", "image-to-video":
			return true
		}
	}
	return false
}

// codeBuddyIDEUA /v3/config 要求能解析出 CodeBuddy 版本号的 UA（否则返回精简目录）。
const codeBuddyIDEUA = "CodeBuddyIDE/4.12.0 CodeBuddy/4.12.0"

// FetchModels 拉取账号可用模型目录（两路并发合并；一路失败降级另一路）。
func (c *Client) FetchModels() ([]ModelInfo, error) {
	type probeResult struct {
		infos []ModelInfo
		err   error
	}
	enterpriseCh := make(chan probeResult, 1)
	v3Ch := make(chan probeResult, 1)
	go func() {
		infos, err := c.fetchEnterpriseModels()
		enterpriseCh <- probeResult{infos, err}
	}()
	go func() {
		infos, err := c.fetchV3Models()
		v3Ch <- probeResult{infos, err}
	}()
	enterprise := <-enterpriseCh
	v3 := <-v3Ch
	if enterprise.err != nil && v3.err != nil {
		return nil, enterprise.err
	}
	out := mergeModelInfos(v3.infos, enterprise.infos)
	if len(out) == 0 {
		return nil, fmt.Errorf("models api returned empty list")
	}
	return out, nil
}

// mergeModelInfos 合并两路目录：primary 优先，secondary 补缺；输出顺序稳定。
func mergeModelInfos(primary, secondary []ModelInfo) []ModelInfo {
	if len(secondary) == 0 {
		return primary
	}
	seen := make(map[string]bool, len(primary)+len(secondary))
	out := make([]ModelInfo, 0, len(primary)+len(secondary))
	for _, mi := range primary {
		if mi.ID == "" || seen[mi.ID] {
			continue
		}
		seen[mi.ID] = true
		out = append(out, mi)
	}
	for _, mi := range secondary {
		if mi.ID == "" || seen[mi.ID] {
			continue
		}
		seen[mi.ID] = true
		out = append(out, mi)
	}
	return out
}

// fetchEnterpriseModels 企业模型端点（agents[cli].models 过滤 + nonChatModel 剔除）。
func (c *Client) fetchEnterpriseModels() ([]ModelInfo, error) {
	endpoint := c.chatBase() + "/console/enterprises/personal/models"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	CommonHeaders(req, c.cred)
	req.Header.Set("Authorization", "Bearer "+c.cred.AccessToken)
	resp, err := jsonHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models api status %d: %s", resp.StatusCode, truncate(string(raw), 120))
	}
	var env struct {
		Code int `json:"code"`
		Data struct {
			Models []dynModelEntry `json:"models"`
			Agents []struct {
				Name   string   `json:"name"`
				Models []string `json:"models"`
			} `json:"agents"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("models parse: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("models api code=%d", env.Code)
	}
	var cliIDs []string
	for _, ag := range env.Data.Agents {
		if ag.Name == "cli" {
			cliIDs = ag.Models
			break
		}
	}
	if len(cliIDs) == 0 {
		return nil, fmt.Errorf("no cli agent models found")
	}
	dynMap := make(map[string]dynModelEntry, len(env.Data.Models))
	for _, m := range env.Data.Models {
		if nonChatModel(m.ID, m.MaxOutputTokens, m.Tags) {
			continue
		}
		dynMap[m.ID] = m
	}
	out := make([]ModelInfo, 0, len(cliIDs))
	for _, id := range cliIDs {
		if m, ok := dynMap[id]; ok && !m.Disabled {
			out = append(out, m.modelInfo())
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("models api returned empty list")
	}
	return out, nil
}

// fetchV3Models /v3/config（IDE UA 完整能力版），全量 models + nonChatModel 剔除。
func (c *Client) fetchV3Models() ([]ModelInfo, error) {
	req, err := http.NewRequest(http.MethodGet, c.chatBase()+"/v3/config", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Authorization", "Bearer "+c.cred.AccessToken)
	if c.cred.UID != "" {
		req.Header.Set("X-User-Id", c.cred.UID)
	}
	domain := c.cred.Domain
	if domain == "" {
		domain = "copilot.tencent.com"
	}
	req.Header.Set("X-Domain", strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://"), "/"))
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("User-Agent", codeBuddyIDEUA)
	req.Header.Set("X-CodeBuddy-Request", "1")
	resp, err := jsonHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("v3/config status %d: %s", resp.StatusCode, truncate(string(raw), 120))
	}
	var env struct {
		Code int `json:"code"`
		Data struct {
			Models []dynModelEntry `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("v3/config parse: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("v3/config code=%d", env.Code)
	}
	out := make([]ModelInfo, 0, len(env.Data.Models))
	for _, m := range env.Data.Models {
		if strings.TrimSpace(m.ID) == "" || m.Disabled {
			continue
		}
		if nonChatModel(m.ID, m.MaxOutputTokens, m.Tags) {
			continue
		}
		out = append(out, m.modelInfo())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("v3/config returned empty models")
	}
	return out, nil
}
