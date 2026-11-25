package controller

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/proxy_pool"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// 内核（mihomo）边车：把机场/客户端节点变成可拨号的本地出口。
//   GET  /api/proxy-pool/runtime          内核状态 + 我的节点与端口
//   POST /api/proxy-pool/runtime/reload   用最新节点重建配置并重启内核
//   POST /api/proxy-pool/runtime/select   切换我的出站节点
// ---------------------------------------------------------------------------

// GetProxyRuntimeStatus GET /api/proxy-pool/runtime
func GetProxyRuntimeStatus(c *gin.Context) {
	uid := c.GetInt("id")
	manager := proxy_pool.ManagerInstance()
	status := manager.Status()
	nodes := manager.NodeNames(uid)
	port, hasPort := status.Ports[uid]
	if !hasPort && len(nodes) > 0 && status.SharedPort > 0 {
		port = status.SharedPort
		hasPort = true
	}
	common.ApiSuccess(c, gin.H{
		"available":     status.Available,
		"running":       status.Running,
		"binary":        status.Binary,
		"proxies":       status.Proxies,
		"public_count":  status.PublicCount,
		"selected_node": status.SelectedNode,
		"last_error":    status.LastError,
		"started_at":    status.StartedAt,
		"port":          port,
		"has_listener":  hasPort,
		"proxy_url":     runtimeProxyURL(port, hasPort),
		"nodes":         nodes,
	})
}

// ReloadProxyRuntime POST /api/proxy-pool/runtime/reload
// 只有管理员能触发全局重启；普通用户触发时也只重建自己的节点视图（数据全量编译，成本低）。
func ReloadProxyRuntime(c *gin.Context) {
	status, err := proxy_pool.ReloadAll()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
			"data":    gin.H{"available": status.Available, "running": status.Running},
		})
		return
	}
	common.ApiSuccess(c, gin.H{
		"available":  status.Available,
		"running":    status.Running,
		"proxies":    status.Proxies,
		"last_error": status.LastError,
	})
}

// SelectProxyRuntimeNode POST /api/proxy-pool/runtime/select
// body: {"name":"u2·极速 专线 香港 01"}
func SelectProxyRuntimeNode(c *gin.Context) {
	uid := c.GetInt("id")
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		common.ApiErrorMsg(c, "node name is required")
		return
	}
	if err := proxy_pool.ManagerInstance().Select(uid, req.Name); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{"selected": req.Name})
}

func runtimeProxyURL(port int, ok bool) string {
	if !ok || port <= 0 {
		return ""
	}
	return "http://127.0.0.1:" + strconv.Itoa(port)
}

// triggerKernelReload coalesces a burst of edits into one running rebuild and
// one trailing rebuild; a panic cannot terminate the process.
var kernelReloadState struct {
	sync.Mutex
	running bool
	pending bool
}

func triggerKernelReload() {
	kernelReloadState.Lock()
	kernelReloadState.pending = true
	if kernelReloadState.running {
		kernelReloadState.Unlock()
		return
	}
	kernelReloadState.running = true
	kernelReloadState.Unlock()
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				common.SysError("mihomo reload recovered from panic")
			}
			kernelReloadState.Lock()
			kernelReloadState.running = false
			kernelReloadState.Unlock()
		}()
		for {
			time.Sleep(time.Second)
			kernelReloadState.Lock()
			kernelReloadState.pending = false
			kernelReloadState.Unlock()
			if _, err := proxy_pool.ReloadAll(); err != nil {
				common.SysError("mihomo reload failed: " + err.Error())
			}
			kernelReloadState.Lock()
			pending := kernelReloadState.pending
			kernelReloadState.Unlock()
			if !pending {
				return
			}
		}
	}()
}
