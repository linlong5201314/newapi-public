package raccoon

// ChannelName 渠道显示名。
const ChannelName = "商汤小浣熊"

// DefaultBaseURL 是小浣熊网页端的 LLM 接口基址。
//
// 真实推理端点是 https://xiaohuanxiong.com/api/web/llm/v2/chat/completions，
// 模型目录是同前缀的 /model_catalog。渠道 BaseURL 留空时用这里，填了则整体覆盖
// （自建反代/企业代理）。
const DefaultBaseURL = "https://xiaohuanxiong.com/api/web/llm/v2"

// ModelList 是内置兜底模型清单（上游 /model_catalog 拉不到时使用）。
// 小浣熊的模型 id 形如 sn-glm-5-3-flash / sn-deepseek-v3-2，按目录原样透传。
var ModelList = []string{
	"sn-glm-5-3-flash",
}

// DefaultTestModel 连通性测试用的模型。
const DefaultTestModel = "sn-glm-5-3-flash"

// BrowserHeaders 是小浣熊网页端用来识别「请求来自自家前端」的头。
// 缺 Origin/Referer 时上游会按跨站请求拒绝，因此默认全部带上。
var BrowserHeaders = map[string]string{
	"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36",
	"Accept":          "application/json, text/event-stream",
	"Accept-Language": "zh-CN,zh;q=0.9",
	"Origin":          "https://xiaohuanxiong.com",
	"Referer":         "https://xiaohuanxiong.com/",
}
