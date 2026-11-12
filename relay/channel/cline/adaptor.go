package cline

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// Adaptor 把 Cline（cline.bot）的官方 API 当成标准 OpenAI Chat Completions 上游。
//
// 与普通 OpenAI 渠道的差别只有两处：
//  1. 上游基址已自带 /api/v1，请求路径要去掉 /v1 前缀，否则拼出 /api/v1/v1/…
//  2. Authorization 必须是 "Bearer workos:<accessToken>"，且必须带 Cline 客户端指纹头
//     （缺指纹头上游返回 403 "only available via Cline product surfaces"）
//
// 流式 SSE 与错误体结构都是 OpenAI 格式，直接复用 openai.Adaptor 的转换/解析。
type Adaptor struct {
	openai.Adaptor
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

// GetRequestURL 用渠道 BaseURL（或默认 https://api.cline.bot/api/v1）拼上游路径。
//
// RelayInfo.RequestURLPath 形如 /v1/chat/completions，而 Cline 的路径是
// /chat/completions，所以这里去掉 /v1 前缀。
func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	base := strings.TrimSuffix(strings.TrimSpace(info.ChannelBaseUrl), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	path := strings.TrimSpace(info.RequestURLPath)
	if path == "" {
		path = "/v1/chat/completions"
	}
	// 只去掉开头的 /v1；/v1beta 之类保持原样（Cline 不支持，但不要拼坏 URL）。
	if strings.HasPrefix(path, "/v1/") {
		path = strings.TrimPrefix(path, "/v1")
	} else if path == "/v1" {
		path = "/chat/completions"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path, nil
}

// SetupRequestHeader 先走 OpenAI 默认逻辑（含 Header Override），再改写成 Cline 形态。
func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	if err := a.Adaptor.SetupRequestHeader(c, header, info); err != nil {
		return err
	}

	// 渠道 key 允许三种写法：裸 accessToken / "workos:xxx" / "Bearer workos:xxx"。
	key := strings.TrimSpace(info.ApiKey)
	key = strings.TrimSpace(strings.TrimPrefix(key, "Bearer "))
	key = strings.TrimSpace(strings.TrimPrefix(key, AuthPrefix))
	if key == "" {
		return fmt.Errorf("cline: 渠道密钥为空，请填写 Cline 的 accessToken（或用 cline_oauth.py 换取）")
	}
	header.Set("Authorization", "Bearer "+AuthPrefix+key)

	// 客户端指纹头（缺了会被 403）。
	for name, value := range ClientFingerprint {
		header.Set(name, value)
	}
	if header.Get("X-Task-ID") == "" {
		header.Set("X-Task-ID", newTaskID())
	}
	return nil
}

// newTaskID 生成 X-Task-ID。Cline 只要求「一次会话一个 id」，用时间戳 + 纳秒
// 拼成 UUID v4 形状即可，不需要真正的随机源。
func newTaskID() string {
	now := time.Now()
	seed := uint64(now.UnixNano())
	var b [16]byte
	for i := 0; i < 8; i++ {
		b[i] = byte(seed >> (8 * uint(i)))
	}
	for i := 8; i < 16; i++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		b[i] = byte(seed >> 56)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
