package raccoon

import (
	"fmt"
	"net/http"
	"strings"

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
