package operation_setting

// 多租户自服务渠道（我的中转站）平台级开关。
// 默认值与 model/option.go InitOptionMap 注册保持一致，DB 中的配置会在启动时覆盖。
var (
	// SelfChannelEnabled 自服务渠道总开关；关闭时 /api/channel/self 全组 404，
	// 但路由层 owner 过滤仍然生效（已有自建渠道的用户请求只会命中自己的渠道）。
	SelfChannelEnabled = true
	// SelfChannelMaxPerUser 每个用户可创建的自建渠道数量上限。
	SelfChannelMaxPerUser = 20
	// SelfChannelUsePublicFallback 用户桶无可用渠道时是否回落到平台公共渠道（owner_id=0）。
	SelfChannelUsePublicFallback = false
	// SelfKeyFreeBilling 命中用户自己渠道时跳过扣费（仅记账，logs 照写）。
	SelfKeyFreeBilling = true
	// PlatformPublicFreeBilling 命中平台公共渠道（owner_id=0）时同样跳过扣费。
	// 与 SelfKeyFreeBilling 组合成"全场景免价格"：个人自建 + 平台公共都不计费。
	PlatformPublicFreeBilling = true
)

// RouteRuleMaxPerUser 每个用户可创建的路由规则数量上限（M2）。
// 默认值与 model/option.go InitOptionMap 注册保持一致。
var RouteRuleMaxPerUser = 20
