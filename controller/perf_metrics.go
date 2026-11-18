package controller

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func perfMetricsRequestGroups(c *gin.Context) ([]string, error) {
	userGroup := ""
	if userID := c.GetInt("id"); userID > 0 {
		user, err := model.GetUserCache(userID)
		if err != nil {
			return nil, err
		}
		userGroup = user.Group
	}
	usable := service.GetUserUsableGroups(userGroup)
	groups := make([]string, 0, len(usable))
	for group := range usable {
		// The virtual auto rollup has no actual selected route group. It must
		// not bypass either group authorization or historical attribution.
		if group != "auto" {
			groups = append(groups, group)
		}
	}
	return groups, nil
}

// unavailablePerfMetricData preserves the reporting window and nil-summary
// contract without publishing legacy model/group buckets. Those buckets contain
// neither channel nor owner IDs: even a currently public model may include past
// private traffic, so a current visibility filter cannot authorize its history.
func unavailablePerfMetricData(c *gin.Context) gin.H {
	hours := 24
	if value, err := strconv.Atoi(c.Query("hours")); err == nil && value > 0 {
		hours = min(value, 24*30)
	}
	end := time.Now().Unix()
	return gin.H{
		"summary":            nil,
		"window_start":       end - end%3600 - int64(hours-1)*3600,
		"window_end":         end,
		"status":             "unknown",
		"metric_scope":       "model_group",
		"unavailable_reason": "missing_channel_dimension",
	}
}

func GetPerfMetricsSummary(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if len(c.QueryArray("recent_model")) > perfmetrics.MaxRecentModels {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	if _, err := perfMetricsRequestGroups(c); err != nil {
		common.ApiError(c, err)
		return
	}
	// Do not query global summaries or recent-model caches: no historical row
	// can be attributed to the authorized channels. Omit all such model items,
	// including untrusted recent_model keys, rather than imply scoped samples.
	data := unavailablePerfMetricData(c)
	data["models"] = []perfmetrics.ModelSummary{}
	data["recent_success_series"] = map[string][]perfmetrics.SuccessRatePoint{}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func GetPerfMetrics(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	modelName := c.Query("model")
	if modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "model is required"})
		return
	}
	groups, err := perfMetricsRequestGroups(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	requestedGroup := c.Query("group")
	visible := false
	for _, group := range groups {
		if requestedGroup != "" && requestedGroup != group {
			continue
		}
		if slices.Contains(model.GetGroupEnabledModelsForOwner(group, c.GetInt("id")), modelName) {
			visible = true
			break
		}
	}
	if !visible {
		// Use the same response for nonexistent, private and unauthorized-group
		// names; never echo an untrusted/private name into the response.
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "model metrics not found"})
		return
	}
	data := unavailablePerfMetricData(c)
	data["model_name"] = modelName
	// Keep the existing wire-schema marker; this is response-only suppression,
	// not a new series schema or an owner/channel attribution migration.
	data["series_schema"] = "dbcd0a3c01b55203"
	data["series"] = []perfmetrics.BucketPoint{}
	data["groups"] = []perfmetrics.GroupResult{}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
