package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 自服务渠道 API（/api/channel/self）
//
// 多租户"我的中转站"：handler 全部强制 owner_id = 当前登录用户。
// 归属不符的资源一律返回 404 而非 403，避免跨租户探测渠道是否存在。
// 创建/更新/删除/测试/拉模型/复制逻辑复用 admin 渠道 controller 的公共执行体。
// ---------------------------------------------------------------------------

// writeSelfChannelNotFound 统一 404 响应：不区分"不存在"与"不属于你"。
func writeSelfChannelNotFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{
		"success": false,
		"message": "channel not found",
	})
	c.Abort()
}

// loadSelfOwnedChannel 加载渠道并校验归属当前用户；不满足时写 404 并返回 nil,false。
func loadSelfOwnedChannel(c *gin.Context, id int, selectAll bool) (*model.Channel, bool) {
	if id <= 0 {
		writeSelfChannelNotFound(c)
		return nil, false
	}
	channel, err := model.GetChannelById(id, selectAll)
	if err != nil || channel == nil || channel.OwnerId != c.GetInt("id") {
		writeSelfChannelNotFound(c)
		return nil, false
	}
	return channel, true
}

// countSelfChannels 统计用户当前自建渠道数量。
func countSelfChannels(uid int) (int64, error) {
	var count int64
	err := model.DB.Model(&model.Channel{}).Where("owner_id = ?", uid).Count(&count).Error
	return count, err
}

// checkSelfChannelLimit 校验 SelfChannelMaxPerUser 上限。
// planned 为本次操作即将创建的渠道数；超限时写好错误响应并返回 false。
func checkSelfChannelLimit(c *gin.Context, planned int) bool {
	uid := c.GetInt("id")
	maxPerUser := operation_setting.SelfChannelMaxPerUser
	if maxPerUser <= 0 {
		maxPerUser = 20
	}
	current, err := countSelfChannels(uid)
	if err != nil {
		common.SysError(common.LogText("failed to count self channels for user %d: %s", uid, err.Error()))
		common.ApiErrorT(c, "Failed to verify channel quota. Please try again later")
		return false
	}
	if int(current)+planned > maxPerUser {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("Self channel limit reached: max %d channels per user", maxPerUser),
		})
		return false
	}
	return true
}

// GetSelfChannels GET /api/channel/self/
// 复用 admin GetAllChannels 的查询构造，强制 owner_id=当前用户，
// 响应附带 max_per_user（读 Option SelfChannelMaxPerUser）。
func GetSelfChannels(c *gin.Context) {
	uid := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	channelData := make([]*model.Channel, 0)
	idSort, _ := strconv.ParseBool(c.Query("id_sort"))
	sortOptions := model.NewChannelSortOptions(c.Query("sort_by"), c.Query("sort_order"), idSort)
	groupFilter := model.NormalizeChannelGroupFilter(c.Query("group"))
	statusFilter := parseStatusFilter(c.Query("status"))
	typeFilter := -1
	if typeStr := c.Query("type"); typeStr != "" {
		if t, err := strconv.Atoi(typeStr); err == nil {
			typeFilter = t
		}
	}

	baseQuery := func(statusF int, typeF int) *gorm.DB {
		return buildChannelListQuery(groupFilter, statusF, typeF).Where("owner_id = ?", uid)
	}

	var total int64
	if err := baseQuery(statusFilter, typeFilter).Count(&total).Error; err != nil {
		common.SysError(common.LogText("failed to count self channels: %s", err.Error()))
		common.ApiErrorT(c, "Failed to count channels. Please try again later")
		return
	}

	err := sortOptions.Apply(baseQuery(statusFilter, typeFilter)).
		Limit(pageInfo.GetPageSize()).
		Offset(pageInfo.GetStartIdx()).
		Omit("key").
		Find(&channelData).Error
	if err != nil {
		common.SysError(common.LogText("failed to get self channels: %s", err.Error()))
		common.ApiErrorT(c, "Failed to fetch channels. Please try again later")
		return
	}

	for _, datum := range channelData {
		clearChannelInfo(datum)
	}

	var results []struct {
		Type  int64
		Count int64
	}
	if err := baseQuery(statusFilter, -1).Select("type, count(*) as count").Group("type").Find(&results).Error; err != nil {
		common.SysError(common.LogText("failed to count self channel types: %s", err.Error()))
		common.ApiErrorT(c, "Failed to count channel types. Please try again later")
		return
	}
	typeCounts := make(map[int64]int64)
	for _, r := range results {
		typeCounts[r.Type] = r.Count
	}

	common.ApiSuccess(c, gin.H{
		"items":        channelData,
		"total":        total,
		"page":         pageInfo.GetPage(),
		"page_size":    pageInfo.GetPageSize(),
		"type_counts":  typeCounts,
		"max_per_user": operation_setting.SelfChannelMaxPerUser,
	})
}

// SearchSelfChannels GET /api/channel/self/search
// 复用 searchChannelsExecute，强制 owner_id=当前用户；query 参数与响应结构
// 与 admin /api/channel/search 完全对齐。
func SearchSelfChannels(c *gin.Context) {
	searchChannelsExecute(c, c.GetInt("id"))
}

// FetchSelfModels POST /api/channel/self/fetch_models
// 编辑抽屉"未保存配置"拉模型：base_url/key 等凭据全部来自请求体，不落库、
// 不读库（强制 ChannelID=0，屏蔽 buildAdvancedCustomModelPreviewChannel 的读库分支）。
func FetchSelfModels(c *gin.Context) {
	var req fetchModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}
	req.ChannelID = 0
	fetchModelsExecute(c, req)
}

// GetSelfChannel GET /api/channel/self/:id
func GetSelfChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		writeSelfChannelNotFound(c)
		return
	}
	channel, ok := loadSelfOwnedChannel(c, id, false)
	if !ok {
		return
	}
	clearChannelInfo(channel)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
}

// AddSelfChannel POST /api/channel/self
// 复用 addChannelExecute；强制 owner_id=当前用户；扣 SelfChannelMaxPerUser 上限。
func AddSelfChannel(c *gin.Context) {
	uid := c.GetInt("id")
	addChannelRequest := AddChannelRequest{}
	if err := c.ShouldBindJSON(&addChannelRequest); err != nil {
		common.ApiError(c, err)
		return
	}
	if addChannelRequest.Channel == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	// 与 admin AddChannel 一致的 task plugin 绑定权限校验（普通用户默认无此权限）。
	if (addChannelRequest.Channel.Type == constant.ChannelTypeTaskPlugin || len(addChannelRequest.Channel.GetSetting().TaskPluginBindings()) > 0) &&
		!authz.Can(uid, c.GetInt("role"), authz.TaskPluginBind) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "task plugin channels require the task_plugin.bind permission",
		})
		return
	}

	// 估算本次将创建的渠道数（与 addChannelExecute 的 mode 展开一致的单/批量口径）
	planned := 1
	if addChannelRequest.Mode == "batch" {
		planned = 0
		for _, key := range strings.Split(addChannelRequest.Channel.Key, "\n") {
			if strings.TrimSpace(key) != "" {
				planned++
			}
		}
		if planned == 0 {
			planned = 1
		}
	}
	if !checkSelfChannelLimit(c, planned) {
		return
	}

	// 强制归属当前用户，客户端传入的 owner_id 一律忽略。
	addChannelRequest.Channel.OwnerId = uid
	addChannelExecute(c, &addChannelRequest)
}

// UpdateSelfChannel PUT /api/channel/self
// 复用 updateChannelExecute：归属不符 404；owner_id 强制保留原值（禁止改归属）。
func UpdateSelfChannel(c *gin.Context) {
	updateChannelExecute(c, c.GetInt("id"))
}

// DeleteSelfChannel DELETE /api/channel/self/:id
func DeleteSelfChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		writeSelfChannelNotFound(c)
		return
	}
	channel, ok := loadSelfOwnedChannel(c, id, false)
	if !ok {
		return
	}
	deleteChannelById(c, channel.Id)
}

// UpdateSelfChannelStatus POST /api/channel/self/:id/status
// 自服务渠道状态切换（启用/手动禁用），强制 owner；账号型渠道「需重新登录」
// 恢复闭环的用户侧出口（更新凭据后手动启用）。
func UpdateSelfChannelStatus(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		writeSelfChannelNotFound(c)
		return
	}
	if _, ok := loadSelfOwnedChannel(c, id, false); !ok {
		return
	}
	req := ChannelStatusRequest{}
	if err := c.ShouldBindJSON(&req); err != nil || !isManageableChannelStatus(req.Status) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	changed := model.UpdateChannelStatus(id, "", req.Status, "manual operation (self)")
	if changed {
		model.InitChannelCache()
		if req.Status != common.ChannelStatusEnabled {
			closeActiveChannelWebSockets([]int{id})
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    changed,
	})
}

// TestSelfChannel POST /api/channel/self/test
// body: {"id": 1, "model": "gpt-4o-mini", "endpoint_type": "", "stream": false}
// id 也兼容 query ?id=；model/endpoint_type/stream 兼容 query 参数。
func TestSelfChannel(c *gin.Context) {
	req := struct {
		Id           int    `json:"id"`
		Model        string `json:"model"`
		EndpointType string `json:"endpoint_type"`
		Stream       *bool  `json:"stream"`
	}{}
	if c.Request != nil && c.Request.Body != nil && c.Request.ContentLength != 0 {
		// 空 body 容错；绑定失败按缺省处理
		_ = c.ShouldBindJSON(&req)
	}
	if req.Id <= 0 {
		req.Id, _ = strconv.Atoi(c.Query("id"))
	}
	if req.Id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	channel, ok := loadSelfOwnedChannel(c, req.Id, true)
	if !ok {
		return
	}
	testModel := req.Model
	if testModel == "" {
		testModel = c.Query("model")
	}
	endpointType := req.EndpointType
	if endpointType == "" {
		endpointType = c.Query("endpoint_type")
	}
	streamParam := c.Query("stream")
	if req.Stream != nil {
		streamParam = strconv.FormatBool(*req.Stream)
	}
	testChannelForRequest(c, channel, testModel, endpointType, streamParam)
}

// FetchSelfChannelModels GET /api/channel/self/fetch_models/:id
func FetchSelfChannelModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		writeSelfChannelNotFound(c)
		return
	}
	channel, ok := loadSelfOwnedChannel(c, id, true)
	if !ok {
		return
	}
	// M3 账号型渠道（workbuddy/trae）：走各自上游模型端点（凭据型，非 OpenAI /models）。
	if _, handled := SelfFetchAccountModels(c, channel); handled {
		return
	}
	ids, err := fetchChannelUpstreamModelIDs(channel)
	if err != nil {
		common.ApiErrorT(c, "Failed to fetch models: {{error}}", map[string]any{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    ids,
	})
}

// CopySelfChannel POST /api/channel/self/copy/:id
// 复制自建渠道（克隆体经浅拷贝继承 owner_id=当前用户）；扣 SelfChannelMaxPerUser 上限。
func CopySelfChannel(c *gin.Context) {
	uid := c.GetInt("id")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		writeSelfChannelNotFound(c)
		return
	}
	origin, ok := loadSelfOwnedChannel(c, id, true)
	if !ok {
		return
	}
	if (origin.Type == constant.ChannelTypeTaskPlugin || len(origin.GetSetting().TaskPluginBindings()) > 0) &&
		!authz.Can(uid, c.GetInt("role"), authz.TaskPluginBind) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "task plugin channels require the task_plugin.bind permission"})
		return
	}
	if !checkSelfChannelLimit(c, 1) {
		return
	}
	copyChannelExecute(c, origin)
}
