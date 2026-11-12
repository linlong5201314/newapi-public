package cline

// ChannelName 渠道显示名。
const ChannelName = "Cline"

// DefaultBaseURL 是 Cline 官方 API 基址。
//
// 注意它已经带了 /api/v1：Cline 的真实推理端点是
// https://api.cline.bot/api/v1/chat/completions，
// 所以渠道里的 BaseURL 留空时用这里，填了则整体覆盖（自建反代/企业代理）。
const DefaultBaseURL = "https://api.cline.bot/api/v1"

// AuthPrefix Cline 用 WorkOS 令牌，Authorization 头必须写成
// "Bearer workos:<accessToken>"。
const AuthPrefix = "workos:"

// ModelList 是内置兜底模型清单（上游 /models 拉不到时使用）。
// Cline 的模型 id 自带供应商前缀（cline-free/…、deepseek/…、cline-pass/…），
// 必须原样透传，不能改写。
var ModelList = []string{
	"cline-free/nemotron-3.5-super-vl",
	"cline-free/mimo-v2.6-flash",
	"cline-free/muse-spark-1.3-contributor",
	"cline-free/solar-mini4",
	"deepseek/deepseek-v4-flash",
	"poolside/laguna-s-2.1:free",
	"zai/glm-5.3-flash",
	"cline-pass/glm-5.2",
	"cline-pass/deepseek-v4-flash",
	"cline-pass/qwen3.7-max",
}

// DefaultTestModel 连通性测试用的模型。
const DefaultTestModel = "deepseek/deepseek-v4-flash"

// ClientFingerprint 是 Cline 官方用来识别「请求来自 Cline 客户端」的头。
//
// 缺这些头时上游会返回 403：
// "deepseek/deepseek-v4-flash is only available via Cline product surfaces"。
// 值对齐 Cline 3.0.47。
var ClientFingerprint = map[string]string{
	"User-Agent":         "Cline/3.0.47",
	"HTTP-Referer":       "https://cline.bot",
	"X-Title":            "Cline",
	"X-IS-MULTIROOT":     "false",
	"X-CLIENT-TYPE":      "cline-sdk",
	"X-CLIENT-VERSION":   "3.0.47",
	"X-PLATFORM":         "terminal",
	"X-PLATFORM-VERSION": "3.0.47",
	"X-CORE-VERSION":     "0.0.66",
	"Accept-Encoding":    "identity",
}
