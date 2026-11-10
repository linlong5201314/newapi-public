// checkin_center.go M3 签到中心平台开关（Option 三项）。
package operation_setting

var (
	// CheckinEnabled 签到中心总开关；关闭时 /api/checkin 全组 404 且调度器不 sweep。
	CheckinEnabled = true
	// CheckinMaxAccountsPerUser 每个用户可登记的签到账号数上限。
	CheckinMaxAccountsPerUser = 10
	// CheckinAllowAgentUrl 是否允许 agent 模式（远程执行器 URL）。
	// 关闭时 agent 模式账号创建/执行一律拒绝（SSRF 面收敛）。
	CheckinAllowAgentUrl = true
)
