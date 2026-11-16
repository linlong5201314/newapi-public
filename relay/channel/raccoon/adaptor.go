package raccoon

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// Adaptor 把小浣熊网页端的 LLM 接口当成标准 OpenAI Chat Completions 上游。
//
// 与普通 OpenAI 渠道的差别只有两处：
//  1. 上游基址自带 /api/web/llm/v2，请求路径要去掉 /v1 前缀，否则拼出
//     /api/web/llm/v2/v1/chat/completions
//  2. Authorization 是网页登录拿到的 JWT（Bearer），且必须带 Origin/Referer，
//     否则上游按跨站请求拒绝
//
// 流式 SSE 与错误体结构都是 OpenAI 格式，直接复用 openai.Adaptor。
type Adaptor struct {
	openai.Adaptor
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

// GetRequestURL 用渠道 BaseURL（或默认 https://xiaohuanxiong.com/api/web/llm/v2）拼上游路径。
func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	base := strings.TrimSuffix(strings.TrimSpace(info.ChannelBaseUrl), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	path := strings.TrimSpace(info.RequestURLPath)
	if path == "" {
		path = "/v1/chat/completions"
	}
	// 先把路径规范化成以 "/" 开头：上游 relay 传进来的可能是 "v1/chat/completions"，
	// 直接拼会变成 .../v2/v1/chat/completions（实测就是这一步导致 404）。
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	switch {
	case strings.HasPrefix(path, "/v1/"):
		path = strings.TrimPrefix(path, "/v1")
	case path == "/v1":
		path = "/chat/completions"
	}
	return base + path, nil
}

// DoRequest 必须由本类型自己实现。
//
// 原因（真踩过的坑）：openai.Adaptor.DoRequest 的接收者是 *openai.Adaptor，
// 里面调用 channel.DoApiRequest(a, …) 时传进去的是 openai 适配器本身，
// 于是 GetRequestURL / SetupRequestHeader 走的是 OpenAI 版本，
// 我们覆写的方法根本不会被调用 —— 表现为请求 URL 里多出 /v1、上游返回 404，
// 同时 Authorization 也没被改写成小浣熊 JWT。这里显式传入 a，才会用本包的实现。
func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

// SetupRequestHeader 先走 OpenAI 默认逻辑（含 Header Override），再改写成小浣熊形态。
func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	if err := a.Adaptor.SetupRequestHeader(c, header, info); err != nil {
		return err
	}
	token := AccessTokenOf(info.ApiKey)
	if token == "" {
		return fmt.Errorf("raccoon: 渠道密钥为空，请先完成浏览器授权（回调链接或 JWT）")
	}
	header.Set("Authorization", "Bearer "+token)
	for name, value := range BrowserHeaders {
		if header.Get(name) == "" {
			header.Set(name, value)
		}
	}
	return nil
}
