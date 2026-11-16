package controller

import (
	"crypto/subtle"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// internalGuard 校验内部调用口令（X-Internal-Secret == CRYPTO_SECRET），
// 只给面板侧 provisioner 这类受信组件使用，不暴露给终端用户。
func internalGuard(c *gin.Context) bool {
	want := strings.TrimSpace(common.CryptoSecret)
	got := strings.TrimSpace(c.GetHeader("X-Internal-Secret"))
	if want == "" || got == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		c.JSON(403, gin.H{"success": false, "message": "forbidden"})
		return false
	}
	return true
}

// InternalPendingBindings GET /api/internal/bindings/pending?limit=5
// 返回待开通的账号绑定（含解密后的账号密码），仅供面板 provisioner 使用。
func InternalPendingBindings(c *gin.Context) {
	if !internalGuard(c) {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "5"))
	rows, err := model.PendingBindings(limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	type item struct {
		Id       int                     `json:"id"`
		UserId   int                     `json:"user_id"`
		Provider string                  `json:"provider"`
		Kind     string                  `json:"kind"`
		Proxy    string                  `json:"proxy"`
		Public   int                     `json:"public"`
		Accounts []model.SelfAccountCred `json:"accounts"`
	}
	out := make([]item, 0, len(rows))
	for i := range rows {
		creds, err := rows[i].DecodeAccounts()
		if err != nil {
			_ = model.MarkBindingStatus(rows[i].Id, "failed", 0, "", "解密账号失败: "+err.Error())
			continue
		}
		out = append(out, item{Id: rows[i].Id, UserId: rows[i].UserId, Provider: rows[i].Provider, Kind: rows[i].Kind, Proxy: rows[i].Proxy, Public: rows[i].Public, Accounts: creds})
	}
	common.ApiSuccess(c, gin.H{"items": out, "total": len(out)})
}

// InternalMarkBinding POST /api/internal/bindings/:id/status
// body: {"status":"provisioning|ready|failed","channelId":12,"serviceRef":"svc_id/domain","error":""}
func InternalMarkBinding(c *gin.Context) {
	if !internalGuard(c) {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	var req struct {
		Status     string `json:"status"`
		ChannelId  int    `json:"channelId"`
		ServiceRef string `json:"serviceRef"`
		Error      string `json:"error"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.Status == "" {
		req.Status = "provisioning"
	}
	if err := model.MarkBindingStatus(id, req.Status, req.ChannelId, req.ServiceRef, req.Error); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// InternalRefreshChannelCache POST /api/internal/refresh-cache
// 面板直接写库后调用，让所有节点立刻重建渠道缓存（否则要等同步周期）。
func InternalRefreshChannelCache(c *gin.Context) {
	if !internalGuard(c) {
		return
	}
	model.InitChannelCache()
	common.ApiSuccess(c, gin.H{"refreshed": true})
}
