package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type listModelsResponse struct {
	Success bool               `json:"success"`
	Data    []dto.OpenAIModels `json:"data"`
	Object  string             `json:"object"`
}

type userModelsResponse struct {
	Success bool     `json:"success"`
	Data    []string `json:"data"`
}

func setupModelListControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	initModelListColumnNames(t)

	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	var dialector gorm.Dialector = sqlite.Open(dsn)
	if matrixDSN := os.Getenv("PUBLIC_MODEL_TEST_DSN"); matrixDSN != "" {
		// Use only a newly created, isolated database: no truncation or cleanup
		// is ever performed, and a populated application schema is rejected.
		require.Equal(t, "1", os.Getenv("PUBLIC_MODEL_TEST_ISOLATED"), "set PUBLIC_MODEL_TEST_ISOLATED=1 only for a dedicated empty test database")
		switch os.Getenv("PUBLIC_MODEL_TEST_DIALECT") {
		case "postgres":
			dialector = postgres.Open(matrixDSN)
			common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
		case "mysql":
			dialector = mysql.Open(matrixDSN)
			common.SetDatabaseTypes(common.DatabaseTypeMySQL, common.DatabaseTypeMySQL)
		default:
			t.Fatal("PUBLIC_MODEL_TEST_DIALECT must be postgres or mysql")
		}
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	require.NoError(t, err)
	if os.Getenv("PUBLIC_MODEL_TEST_DSN") != "" {
		tables, err := db.Migrator().GetTables()
		require.NoError(t, err)
		require.Empty(t, tables, "refusing to alter a nonempty database; provision a fresh isolated test database")
		// Initialize the model package's dialect quoting through its normal entry
		// point, after the empty-database gate above has proved this is isolated.
		require.NoError(t, db.AutoMigrate(&model.Option{}))
		originalMaster := common.IsMasterNode
		common.IsMasterNode = false
		t.Setenv("SQL_DSN", os.Getenv("PUBLIC_MODEL_TEST_DSN"))
		err = model.InitDB()
		common.IsMasterNode = originalMaster
		require.NoError(t, err)
		initialSQL, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, initialSQL.Close())
		db = model.DB
	}
	model.DB = db
	model.LOG_DB = db

	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}))

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

// seedModelListPlatformChannels supplies the real channel side of ability fixtures.
func seedModelListPlatformChannels(t *testing.T, db *gorm.DB) {
	t.Helper()
	var abilities []model.Ability
	require.NoError(t, db.Find(&abilities).Error)
	channels := make(map[int]*model.Channel)
	for _, ability := range abilities {
		channel := channels[ability.ChannelId]
		if channel == nil {
			channel = &model.Channel{Id: ability.ChannelId, Name: fmt.Sprintf("platform-%d", ability.ChannelId), Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled}
			channels[ability.ChannelId] = channel
		}
		channel.Models += "," + ability.Model
		channel.Group += "," + ability.Group
	}
	for _, channel := range channels {
		channel.Models = strings.TrimPrefix(channel.Models, ",")
		channel.Group = strings.TrimPrefix(channel.Group, ",")
		require.NoError(t, db.Create(channel).Error)
	}
}

func initModelListColumnNames(t *testing.T) {
	t.Helper()

	originalIsMasterNode := common.IsMasterNode
	originalSQLitePath := common.SQLitePath
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	originalSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")
	defer func() {
		common.IsMasterNode = originalIsMasterNode
		common.SQLitePath = originalSQLitePath
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		if hadSQLDSN {
			require.NoError(t, os.Setenv("SQL_DSN", originalSQLDSN))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
	}()

	common.IsMasterNode = false
	common.SQLitePath = fmt.Sprintf("file:%s_init?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, os.Setenv("SQL_DSN", "local"))

	initDB, err := gorm.Open(sqlite.Open(common.SQLitePath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, initDB.AutoMigrate(&model.Option{}))
	initSQL, err := initDB.DB()
	require.NoError(t, err)
	defer initSQL.Close()
	require.NoError(t, model.InitDB())
	if model.DB != nil {
		sqlDB, err := model.DB.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	}
}

func withTieredBillingConfig(t *testing.T, modes map[string]string, exprs map[string]string) {
	t.Helper()

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		if strings.HasPrefix(key, "billing_setting.") {
			saved[key] = value
		}
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
		model.InvalidatePricingCache()
	})

	modeBytes, err := common.Marshal(modes)
	require.NoError(t, err)
	exprBytes, err := common.Marshal(exprs)
	require.NoError(t, err)

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": string(modeBytes),
		"billing_setting.billing_expr": string(exprBytes),
	}))
	model.InvalidatePricingCache()
}

func withSelfUseModeDisabled(t *testing.T) {
	t.Helper()

	original := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = false
	t.Cleanup(func() {
		operation_setting.SelfUseModeEnabled = original
	})
}

func withSelfUseModeEnabled(t *testing.T) {
	t.Helper()

	original := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() {
		operation_setting.SelfUseModeEnabled = original
	})
}

func decodeListModelsPayload(t *testing.T, recorder *httptest.ResponseRecorder) listModelsResponse {
	t.Helper()

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload listModelsResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Equal(t, "list", payload.Object)
	return payload
}

func decodeListModelsResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]struct{} {
	t.Helper()

	payload := decodeListModelsPayload(t, recorder)
	ids := make(map[string]struct{}, len(payload.Data))
	for _, item := range payload.Data {
		ids[item.Id] = struct{}{}
	}
	return ids
}

func pricingByModelName(pricings []model.Pricing) map[string]model.Pricing {
	byName := make(map[string]model.Pricing, len(pricings))
	for _, pricing := range pricings {
		byName[pricing.ModelName] = pricing
	}
	return byName
}

func decodeUserModelsResponse(t *testing.T, recorder *httptest.ResponseRecorder) []string {
	t.Helper()

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload userModelsResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	return payload.Data
}

func TestGetUserModelsFiltersByRequestedGroup(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1002,
		Username: "playground-model-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-default-only-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-disabled-model", ChannelId: 1, Enabled: false},
	}).Error)
	seedModelListPlatformChannels(t, db)

	defaultRecorder := httptest.NewRecorder()
	defaultContext, _ := gin.CreateTestContext(defaultRecorder)
	defaultContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/models?group=default", nil)
	defaultContext.Set("id", 1002)

	GetUserModels(defaultContext)

	defaultModels := decodeUserModelsResponse(t, defaultRecorder)
	require.ElementsMatch(t, []string{"zz-default-only-model"}, defaultModels)

	vipRecorder := httptest.NewRecorder()
	vipContext, _ := gin.CreateTestContext(vipRecorder)
	vipContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/models?group=vip", nil)
	vipContext.Set("id", 1002)

	GetUserModels(vipContext)

	require.Empty(t, decodeUserModelsResponse(t, vipRecorder))
}

func TestGetUserModelsExpandsAutoGroupsInConfiguredOrder(t *testing.T) {
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalSpecialGroups := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		specialGroups := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
		specialGroups.Clear()
		specialGroups.AddAll(originalSpecialGroups)
	})

	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["vip","default","unavailable"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"auto":"自动分组","default":"默认分组","unavailable":"不可用分组"}`))
	specialGroups := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
	specialGroups.Clear()
	specialGroups.Set("default", map[string]string{
		"+:vip":         "VIP 分组",
		"-:unavailable": "",
	})

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1003,
		Username: "playground-auto-model-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "vip", Model: "zz-vip-model", ChannelId: 1, Enabled: true},
		{Group: "vip", Model: "zz-shared-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-default-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-shared-model", ChannelId: 2, Enabled: true},
		{Group: "unavailable", Model: "zz-unavailable-model", ChannelId: 1, Enabled: true},
	}).Error)
	seedModelListPlatformChannels(t, db)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/user/models?group=auto", nil)
	context.Set("id", 1003)

	GetUserModels(context)

	models := decodeUserModelsResponse(t, recorder)
	require.Len(t, models, 3)
	assert.ElementsMatch(t, []string{"zz-vip-model", "zz-shared-model"}, models[:2])
	assert.Equal(t, "zz-default-model", models[2])
}

func TestListModelsIncludesTieredBillingModel(t *testing.T) {
	withSelfUseModeDisabled(t)
	withTieredBillingConfig(t, map[string]string{
		"zz-tiered-visible-model":      "tiered_expr",
		"zz-tiered-empty-expr-model":   "tiered_expr",
		"zz-tiered-missing-expr-model": "tiered_expr",
	}, map[string]string{
		"zz-tiered-visible-model":    `tier("base", p * 1 + c * 2)`,
		"zz-tiered-empty-expr-model": "   ",
	})

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1001,
		Username: "model-list-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-tiered-visible-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-tiered-empty-expr-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-tiered-missing-expr-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-unpriced-model", ChannelId: 1, Enabled: true},
	}).Error)
	seedModelListPlatformChannels(t, db)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set("id", 1001)

	ListModels(ctx, constant.ChannelTypeOpenAI)

	ids := decodeListModelsResponse(t, recorder)
	require.Contains(t, ids, "zz-tiered-visible-model")
	require.NotContains(t, ids, "zz-tiered-empty-expr-model")
	require.NotContains(t, ids, "zz-tiered-missing-expr-model")
	require.NotContains(t, ids, "zz-unpriced-model")

	pricingByName := pricingByModelName(model.GetPricing())
	visiblePricing, ok := pricingByName["zz-tiered-visible-model"]
	require.True(t, ok)
	require.Equal(t, "tiered_expr", visiblePricing.BillingMode)
	require.NotEmpty(t, visiblePricing.BillingExpr)

	emptyExprPricing, ok := pricingByName["zz-tiered-empty-expr-model"]
	require.True(t, ok)
	require.Empty(t, emptyExprPricing.BillingMode)
	require.Empty(t, emptyExprPricing.BillingExpr)

	missingExprPricing, ok := pricingByName["zz-tiered-missing-expr-model"]
	require.True(t, ok)
	require.Empty(t, missingExprPricing.BillingMode)
	require.Empty(t, missingExprPricing.BillingExpr)
}

func TestListModelsUsesAdvancedCustomEndpointTypesFromPricingCache(t *testing.T) {
	withSelfUseModeEnabled(t)
	db := setupModelListControllerTestDB(t)

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		model.InvalidatePricingCache()
	})

	require.NoError(t, db.Create(&model.User{
		Id:       1003,
		Username: "advanced-custom-model-list-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)

	channel := &model.Channel{
		Id:     701,
		Type:   constant.ChannelTypeAdvancedCustom,
		Key:    "advanced-custom-key",
		Status: common.ChannelStatusEnabled,
		Name:   "advanced-custom-channel",
		Group:  "default",
		Models: "gemini-3.5-flash",
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{
			Routes: []dto.AdvancedCustomRoute{
				{
					IncomingPath: "/v1/chat/completions",
					UpstreamPath: "/v1/chat/completions",
				},
				{
					IncomingPath: "/v1/responses",
					UpstreamPath: "/v1beta/models/{model}:generateContent",
					Converter:    "openai_responses_to_gemini_generate_content",
					Models:       []string{"re:^gemini-"},
				},
			},
		},
	})
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     "gemini-3.5-flash",
		ChannelId: 701,
		Enabled:   true,
	}).Error)

	model.InitChannelCache()
	model.GetPricing()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set("id", 1003)

	ListModels(ctx, constant.ChannelTypeOpenAI)

	payload := decodeListModelsPayload(t, recorder)
	require.Len(t, payload.Data, 1)
	require.Equal(t, "gemini-3.5-flash", payload.Data[0].Id)
	require.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, payload.Data[0].SupportedEndpointTypes)
}

func TestListModelsTokenLimitIncludesTieredBillingModel(t *testing.T) {
	withSelfUseModeDisabled(t)
	withTieredBillingConfig(t, map[string]string{
		"zz-token-tiered-visible-model":      "tiered_expr",
		"zz-token-tiered-empty-expr-model":   "tiered_expr",
		"zz-token-tiered-missing-expr-model": "tiered_expr",
	}, map[string]string{
		"zz-token-tiered-visible-model":    `tier("base", p * 1 + c * 2)`,
		"zz-token-tiered-empty-expr-model": "",
	})
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-token-tiered-visible-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-token-tiered-empty-expr-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-token-tiered-missing-expr-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-token-unpriced-model", ChannelId: 1, Enabled: true},
	}).Error)
	seedModelListPlatformChannels(t, db)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set("id", 1001)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{
		"zz-token-tiered-visible-model":      true,
		"zz-token-tiered-empty-expr-model":   true,
		"zz-token-tiered-missing-expr-model": true,
		"zz-token-unpriced-model":            true,
	})

	ListModels(ctx, constant.ChannelTypeOpenAI)

	ids := decodeListModelsResponse(t, recorder)
	require.Contains(t, ids, "zz-token-tiered-visible-model")
	require.NotContains(t, ids, "zz-token-tiered-empty-expr-model")
	require.NotContains(t, ids, "zz-token-tiered-missing-expr-model")
	require.NotContains(t, ids, "zz-token-unpriced-model")
}

func TestListModelsTokenLimitUsesResolvedCustomAutoGroups(t *testing.T) {
	withSelfUseModeEnabled(t)
	originalMax := setting.GetMaxTokenAutoGroups()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("5"))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMax)))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "vip", Model: "zz-vip-allowed", ChannelId: 1, Enabled: true},
		{Group: "vip", Model: "zz-vip-denied", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-default-outside-snapshot", ChannelId: 1, Enabled: true},
	}).Error)
	seedModelListPlatformChannels(t, db)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set("id", 1001)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "auto")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{
		"zz-vip-allowed":              true,
		"zz-default-outside-snapshot": true,
		"zz-not-enabled":              true,
	})

	ListModels(ctx, constant.ChannelTypeOpenAI)
	ids := decodeListModelsResponse(t, recorder)
	require.Equal(t, map[string]struct{}{"zz-vip-allowed": {}}, ids)

	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	emptyRecorder := httptest.NewRecorder()
	emptyCtx, _ := gin.CreateTestContext(emptyRecorder)
	emptyCtx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	emptyCtx.Set("id", 1001)
	common.SetContextKey(emptyCtx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(emptyCtx, constant.ContextKeyTokenGroup, "auto")
	common.SetContextKey(emptyCtx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	common.SetContextKey(emptyCtx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(emptyCtx, constant.ContextKeyTokenModelLimit, map[string]bool{"zz-vip-allowed": true})

	require.NotPanics(t, func() {
		ListModels(emptyCtx, constant.ChannelTypeAnthropic)
	})
	var anthropicResponse struct {
		Data    []dto.AnthropicModel `json:"data"`
		FirstID string               `json:"first_id"`
		LastID  string               `json:"last_id"`
	}
	require.NoError(t, common.Unmarshal(emptyRecorder.Body.Bytes(), &anthropicResponse))
	require.Empty(t, anthropicResponse.Data)
	require.Empty(t, anthropicResponse.FirstID)
	require.Empty(t, anthropicResponse.LastID)
}

func TestModelDirectoryTenantIsolation(t *testing.T) {
	originalGroups := setting.UserUsableGroups2JSONString()
	originalSpecial := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Clear()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalGroups))
		ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.AddAll(originalSpecial)
	})
	db := setupModelListControllerTestDB(t)
	withSelfUseModeEnabled(t)
	require.NoError(t, db.AutoMigrate(&model.PublicPoolComment{}))
	require.NoError(t, db.Create(&[]model.User{
		{Id: 8101, Username: "directory-alice", AffCode: "diralice", Group: "default", Status: common.UserStatusEnabled},
		{Id: 8102, Username: "directory-bob", AffCode: "dirbob", Group: "default", Status: common.UserStatusEnabled},
	}).Error)
	channels := []model.Channel{
		{Id: 8101, OwnerId: 8101, Name: "alice-private", Models: "alice-secret", Group: "default", Status: common.ChannelStatusEnabled},
		{Id: 8102, OwnerId: 8102, Name: "bob-private", Models: "gpt-4o", Group: "default", Status: common.ChannelStatusEnabled},
		{Id: 8103, OwnerId: 0, Name: "platform-free", Tier: "free", Models: "platform-free-model", Group: "default", Status: common.ChannelStatusEnabled},
		{Id: 8104, OwnerId: 8102, Visibility: 1, Name: "bob-shared", Models: "shared-model,shared-secret", OtherInfo: `{"publicModels":["shared-model"]}`, Group: "default", Status: common.ChannelStatusEnabled},
		{Id: 8105, OwnerId: 8102, Visibility: 1, Name: "disabled-shared", Models: "disabled-model", Group: "default", Status: common.ChannelStatusManuallyDisabled},
		{Id: 8106, OwnerId: 8102, Visibility: 1, Name: "vip-shared", Models: "vip-model", Group: "vip", Status: common.ChannelStatusEnabled},
		{Id: 8107, OwnerId: 8102, Visibility: 1, Name: "disabled-ability", Models: "disabled-ability-model", Group: "default", Status: common.ChannelStatusEnabled},
	}
	require.NoError(t, db.Create(&channels).Error)
	for _, channel := range channels {
		for name := range strings.SplitSeq(channel.Models, ",") {
			require.NoError(t, db.Create(&model.Ability{Group: channel.Group, Model: name, ChannelId: channel.Id, Enabled: channel.Id != 8107}).Error)
		}
	}
	originalCache := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = originalCache })
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache=%t", cached), func(t *testing.T) {
			common.MemoryCacheEnabled = cached
			model.InitChannelCache()
			for _, viewer := range []struct {
				id      int
				visible []string
				hidden  []string
			}{
				{8101, []string{"alice-secret", "platform-free-model", "shared-model"}, []string{"gpt-4o", "shared-secret", "disabled-model", "vip-model", "disabled-ability-model"}},
				{8102, []string{"gpt-4o", "platform-free-model", "shared-model", "shared-secret"}, []string{"alice-secret", "disabled-model", "vip-model", "disabled-ability-model"}},
			} {
				listed := model.GetGroupEnabledModelsForOwner("default", viewer.id)
				assert.ElementsMatch(t, viewer.visible, listed)
				recorder := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(recorder)
				ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/self/models?group=default", nil)
				ctx.Set("id", viewer.id)
				GetUserModels(ctx)
				assert.ElementsMatch(t, viewer.visible, decodeUserModelsResponse(t, recorder))
				for _, name := range viewer.visible {
					channel, err := model.GetRandomSatisfiedChannel("default", name, 0, nil, viewer.id)
					require.NoError(t, err)
					assert.NotNil(t, channel, "visible model %s must be routable to owner %d", name, viewer.id)
				}
				for _, name := range viewer.hidden {
					channel, err := model.GetRandomSatisfiedChannel("default", name, 0, nil, viewer.id)
					require.NoError(t, err)
					assert.Nil(t, channel, "private, disabled or wrong-group model %s must not route for owner %d", name, viewer.id)
				}
			}
		})
	}
	// Prime metrics for a private source. Public metrics must not reveal it.
	model.RecordChannelOutcome(8102, true, 17)
	publicRecorder := httptest.NewRecorder()
	publicContext, _ := gin.CreateTestContext(publicRecorder)
	publicContext.Request = httptest.NewRequest(http.MethodGet, "/api/public_pool/models", nil)
	publicContext.Set("id", 8101)
	GetPublicPoolModels(publicContext)
	var publicPayload struct {
		Success bool `json:"success"`
		Data    struct {
			Items []publicModelAgg `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(publicRecorder.Body.Bytes(), &publicPayload))
	require.True(t, publicPayload.Success, publicRecorder.Body.String())
	publicNames := make([]string, 0)
	for _, item := range publicPayload.Data.Items {
		publicNames = append(publicNames, item.Model)
		assert.Nil(t, item.FastestMs)
		for _, channel := range item.Channels {
			assert.NotEqual(t, 8102, channel.ChannelId)
			assert.Nil(t, channel.Throughput)
			assert.Nil(t, channel.TPS)
			assert.Nil(t, channel.LatencyMs)
			assert.Nil(t, channel.SampleCount)
			assert.Nil(t, channel.SampledAt)
			assert.Equal(t, "unknown", channel.Status)
			assert.True(t, channel.Enabled)
			if channel.ChannelId == 8103 {
				assert.Equal(t, "platform_free", channel.SourceKind)
			} else {
				assert.Equal(t, "user_shared", channel.SourceKind)
			}
		}
	}
	assert.ElementsMatch(t, []string{"platform-free-model", "shared-model"}, publicNames)
	// The same-name private channel's provider/endpoint metadata must not enter
	// another user's cached model response.
	for _, viewer := range []int{8102, 8101, 8102} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		ctx.Set("id", viewer)
		common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
		ListModels(ctx, constant.ChannelTypeOpenAI)
		ids := decodeListModelsResponse(t, recorder)
		if viewer == 8101 {
			assert.NotContains(t, ids, "gpt-4o")
			assert.NotContains(t, ids, "shared-secret")
		} else {
			assert.Contains(t, ids, "gpt-4o")
		}
	}
	for _, viewer := range []int{8102, 8101, 8102, 0} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
		if viewer > 0 {
			ctx.Set("id", viewer)
		}
		GetPricing(ctx)
		var payload struct {
			Success bool            `json:"success"`
			Data    []model.Pricing `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
		require.True(t, payload.Success)
		names := pricingByModelName(payload.Data)
		if viewer == 8102 {
			assert.Contains(t, names, "gpt-4o")
		} else {
			assert.NotContains(t, names, "gpt-4o")
			assert.NotContains(t, names, "shared-secret")
		}
	}
	// A private static model is not a metadata/detail bypass for another tenant.
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models/gpt-4o", nil)
	ctx.Set("id", 8101)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	ctx.Params = gin.Params{{Key: "model", Value: "gpt-4o"}}
	RetrieveModel(ctx, constant.ChannelTypeOpenAI)
	assert.Contains(t, recorder.Body.String(), "model_not_found")
	assert.NotContains(t, recorder.Body.String(), `"object":"model"`)
	// Public comments must disappear when their source ceases to be public.
	require.NoError(t, db.Create(&model.PublicPoolComment{Model: "gpt-4o", ChannelId: 8102, ChannelName: "bob-private", UserId: 8102, Content: "private-history"}).Error)
	require.NoError(t, db.Create(&model.PublicPoolComment{Model: "shared-model", ChannelId: 8104, UserId: 8102, Content: "public-comment"}).Error)
	comments, err := model.ListPublicComments("", 50)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	assert.Equal(t, "public-comment", comments[0].Content)
	// Returning a user-supplied path ID must not switch the selector's tenant.
	selectorRecorder := httptest.NewRecorder()
	selectorContext, _ := gin.CreateTestContext(selectorRecorder)
	selectorContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/self/models?group=default", nil)
	selectorContext.Set("id", 8101)
	selectorContext.Params = gin.Params{{Key: "id", Value: "8102"}}
	GetUserModels(selectorContext)
	assert.NotContains(t, decodeUserModelsResponse(t, selectorRecorder), "gpt-4o")
	// A visible custom model has a detail; a token restriction must deny it.
	for _, restricted := range []bool{false, true} {
		detailRecorder := httptest.NewRecorder()
		detailContext, _ := gin.CreateTestContext(detailRecorder)
		detailContext.Request = httptest.NewRequest(http.MethodGet, "/v1/models/shared-model", nil)
		detailContext.Set("id", 8101)
		detailContext.Params = gin.Params{{Key: "model", Value: "shared-model"}}
		common.SetContextKey(detailContext, constant.ContextKeyUserGroup, "default")
		common.SetContextKey(detailContext, constant.ContextKeyTokenModelLimitEnabled, restricted)
		common.SetContextKey(detailContext, constant.ContextKeyTokenModelLimit, map[string]bool{})
		RetrieveModel(detailContext, constant.ChannelTypeOpenAI)
		if restricted {
			assert.Contains(t, detailRecorder.Body.String(), "model_not_found")
		} else {
			assert.Contains(t, detailRecorder.Body.String(), `"id":"shared-model"`)
		}
	}
	// Withdraw shared visibility without recreating the channel cache index.
	common.MemoryCacheEnabled = true
	model.InitChannelCache()
	shared := channels[3]
	shared.Visibility = 0
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", shared.Id).Update("visibility", 0).Error)
	model.CacheUpdateChannel(&shared)
	channel, err := model.GetRandomSatisfiedChannel("default", "shared-model", 0, nil, 8101)
	require.NoError(t, err)
	assert.Nil(t, channel)
	channel, err = model.GetRandomSatisfiedChannel("default", "shared-model", 0, nil, 8102)
	require.NoError(t, err)
	assert.NotNil(t, channel)
	comments, err = model.ListPublicComments("shared-model", 50)
	require.NoError(t, err)
	assert.Empty(t, comments)

}

func TestPerfMetricsTenantIsolation(t *testing.T) {
	originalGroups := setting.UserUsableGroups2JSONString()
	originalSpecial := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))
	ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Clear()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
		ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.AddAll(originalSpecial)
	})
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.PerfMetric{}))
	require.NoError(t, db.Create(&[]model.User{
		{Id: 8201, Username: "perf-alice", AffCode: "perfalice", Group: "default", Status: common.UserStatusEnabled},
		{Id: 8202, Username: "perf-bob", AffCode: "perfbob", Group: "default", Status: common.UserStatusEnabled},
	}).Error)
	channels := []model.Channel{
		{Id: 8201, OwnerId: 8201, Name: "alice-private", Models: "perf-alice-secret", Group: "default", Status: common.ChannelStatusEnabled},
		{Id: 8202, OwnerId: 8202, Name: "bob-private", Models: "perf-bob-secret,perf-mixed-model", Group: "default", Status: common.ChannelStatusEnabled},
		{Id: 8203, OwnerId: 0, Name: "platform", Tier: "free", Models: "perf-platform-model", Group: "default", Status: common.ChannelStatusEnabled},
		{Id: 8204, OwnerId: 8202, Visibility: 1, Name: "bob-shared", Models: "perf-shared-model,perf-mixed-model,perf-unshared-model", OtherInfo: `{"publicModels":["perf-shared-model","perf-mixed-model"]}`, Group: "default", Status: common.ChannelStatusEnabled},
		{Id: 8205, OwnerId: 0, Name: "vip-platform", Models: "perf-vip-model", Group: "vip", Status: common.ChannelStatusEnabled},
	}
	require.NoError(t, db.Create(&channels).Error)
	bucket := time.Now().Unix()
	bucket -= bucket % 3600
	for _, channel := range channels {
		for name := range strings.SplitSeq(channel.Models, ",") {
			require.NoError(t, db.Create(&model.Ability{Group: channel.Group, Model: name, ChannelId: channel.Id, Enabled: true}).Error)
			metric := &model.PerfMetric{ModelName: name, Group: channel.Group, BucketTs: bucket, RequestCount: 7, SuccessCount: 7, TotalLatencyMs: 700, OutputTokens: 700, GenerationMs: 7000}
			require.NoError(t, model.UpsertPerfMetric(metric))
		}
	}
	// Legacy rollups also contain a virtual auto group with no per-route group.
	require.NoError(t, model.UpsertPerfMetric(&model.PerfMetric{ModelName: "perf-auto-private", Group: "auto", BucketTs: bucket, RequestCount: 1, SuccessCount: 1}))
	// Explicitly enable sampling instead of relying on global defaults from other tests.
	savedPerfConfig := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		if strings.HasPrefix(key, "perf_metrics_setting.") {
			savedPerfConfig[key] = value
		}
		return nil
	}))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(savedPerfConfig)) })
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"perf_metrics_setting.enabled": "true"}))
	// A hot-only private model must not bypass filtering through recent_model.
	perfmetrics.Record(perfmetrics.Sample{Model: "perf-bob-hot-secret", Group: "default", Success: true, LatencyMs: 42})
	primed, err := perfmetrics.Query(perfmetrics.QueryParams{Model: "perf-bob-hot-secret", AllowedGroups: []string{"default"}})
	require.NoError(t, err)
	require.NotNil(t, primed.Summary, "fixture must actually prime the shared hot metric cache")
	for _, viewer := range []int{8202, 8201, 0, 8201} {
		t.Run(fmt.Sprintf("summary-viewer=%d", viewer), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/perf-metrics/summary?recent_model=perf-bob-secret&recent_model=perf-bob-hot-secret&recent_model=perf-mixed-model", nil)
			if viewer > 0 {
				ctx.Set("id", viewer)
			}
			GetPerfMetricsSummary(ctx)
			require.Equal(t, http.StatusOK, recorder.Code)
			var payload struct {
				Success bool `json:"success"`
				Data    struct {
					perfmetrics.SummaryAllResult
					Status            string `json:"status"`
					MetricScope       string `json:"metric_scope"`
					UnavailableReason string `json:"unavailable_reason"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
			require.True(t, payload.Success)
			assert.Nil(t, payload.Data.Summary, "a global summary mixes unauthorized private traffic")
			assert.Empty(t, payload.Data.Models, "unattributed historical model aggregates cannot be published")
			assert.Empty(t, payload.Data.RecentSuccessSeries)
			assert.Equal(t, "unknown", payload.Data.Status)
			assert.Equal(t, "model_group", payload.Data.MetricScope)
			assert.Equal(t, "missing_channel_dimension", payload.Data.UnavailableReason)
			for _, secret := range []string{"perf-vip-model", "perf-bob-hot-secret", "perf-auto-private"} {
				assert.NotContains(t, recorder.Body.String(), secret)
			}
			if viewer != 8202 {
				assert.NotContains(t, recorder.Body.String(), "perf-bob-secret")
				assert.NotContains(t, recorder.Body.String(), "perf-unshared-model")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		viewer int
		query  string
		status int
	}{
		{"alice cannot query bob", 8201, "model=perf-bob-secret", http.StatusNotFound},
		{"anonymous cannot query bob", 0, "model=perf-bob-secret", http.StatusNotFound},
		{"subset is not public", 8201, "model=perf-unshared-model", http.StatusNotFound},
		{"group ratio is not group permission", 8201, "model=perf-vip-model&group=vip", http.StatusNotFound},
		{"public model cannot expand group", 8201, "model=perf-shared-model&group=vip", http.StatusNotFound},
		{"auto rollup is not route authorization", 0, "model=perf-platform-model&group=auto", http.StatusNotFound},
		{"bob own legacy aggregate unknown", 8202, "model=perf-bob-secret", http.StatusOK},
		{"platform anonymous unknown", 0, "model=perf-platform-model", http.StatusOK},
		{"explicit shared anonymous unknown", 0, "model=perf-shared-model", http.StatusOK},
		{"same name mixed private unknown", 8201, "model=perf-mixed-model&group=default", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/perf-metrics?"+tc.query, nil)
			if tc.viewer > 0 {
				ctx.Set("id", tc.viewer)
			}
			GetPerfMetrics(ctx)
			require.Equal(t, tc.status, recorder.Code)
			if tc.status != http.StatusOK {
				assert.NotContains(t, recorder.Body.String(), "perf-")
				return
			}
			var payload struct {
				Success bool `json:"success"`
				Data    struct {
					perfmetrics.QueryResult
					Status            string `json:"status"`
					MetricScope       string `json:"metric_scope"`
					UnavailableReason string `json:"unavailable_reason"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
			require.True(t, payload.Success)
			assert.Nil(t, payload.Data.Summary)
			assert.Empty(t, payload.Data.Series)
			assert.Empty(t, payload.Data.Groups)
			assert.Equal(t, "unknown", payload.Data.Status)
			assert.Equal(t, "model_group", payload.Data.MetricScope)
			assert.Equal(t, "missing_channel_dimension", payload.Data.UnavailableReason)
		})
	}
}

func TestPublicPoolRelayLogMetrics(t *testing.T) {
	originalGroups := setting.UserUsableGroups2JSONString()
	originalSpecial := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Clear()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalGroups))
		ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.AddAll(originalSpecial)
	})
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.PublicPoolComment{}))
	require.NoError(t, db.Create(&[]model.User{
		{Id: 8301, Username: "metrics-alice", AffCode: "metalice", Group: "default", Status: common.UserStatusEnabled},
		{Id: 8302, Username: "metrics-bob", AffCode: "metbob", Group: "default", Status: common.UserStatusEnabled},
	}).Error)
	channels := []model.Channel{
		{Id: 8301, OwnerId: 8301, Name: "private", Models: "logged-mixed,logged-platform-model", Group: "default", Status: common.ChannelStatusEnabled},
		{Id: 8302, OwnerId: 8302, Visibility: 0, Name: "shared", Models: "logged-mixed,logged-secret", OtherInfo: `{"publicModels":["logged-mixed"]}`, Group: "default", Status: common.ChannelStatusEnabled},
		{Id: 8303, OwnerId: 0, Tier: "free", Name: "platform", Models: "logged-platform-model,logged-unsampled,logged-zero-duration", Group: "default", Status: common.ChannelStatusEnabled},
	}
	require.NoError(t, db.Create(&channels).Error)
	for _, channel := range channels {
		for name := range strings.SplitSeq(channel.Models, ",") {
			require.NoError(t, db.Create(&model.Ability{Group: channel.Group, Model: name, ChannelId: channel.Id, Enabled: true}).Error)
		}
	}
	// Logs are intentionally separate: a main-DB join cannot authorize them.
	logDB, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_logs?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	originalLogDB := model.LOG_DB
	model.LOG_DB = logDB
	t.Cleanup(func() {
		model.LOG_DB = originalLogDB
		sqlDB, err := logDB.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	now := time.Now().Unix()
	logs := []model.Log{
		{ChannelId: 8302, ModelName: "logged-mixed", Group: "default", Type: model.LogTypeConsume, CreatedAt: now - 60, CompletionTokens: 100, UseTime: 2},
		{ChannelId: 8302, ModelName: "logged-mixed", Group: "default", Type: model.LogTypeConsume, CreatedAt: now - 30, CompletionTokens: 200, UseTime: 4},
		{ChannelId: 8301, ModelName: "logged-mixed", Group: "default", Type: model.LogTypeConsume, CreatedAt: now - 20, CompletionTokens: 9000, UseTime: 100},
		{ChannelId: 8302, ModelName: "logged-secret", Group: "default", Type: model.LogTypeConsume, CreatedAt: now - 10, CompletionTokens: 9000, UseTime: 100},
		{ChannelId: 8302, ModelName: "logged-mixed", Group: "vip", Type: model.LogTypeConsume, CreatedAt: now - 10, CompletionTokens: 9000, UseTime: 100},
		{ChannelId: 8302, ModelName: "logged-mixed", Group: "default", Type: model.LogTypeConsume, CreatedAt: now - 90000, CompletionTokens: 9000, UseTime: 100},
		{ChannelId: 8302, ModelName: "logged-mixed", Group: "default", Type: model.LogTypeError, CreatedAt: now - 5, CompletionTokens: 9000, UseTime: 100},
		{ChannelId: 8303, ModelName: "logged-zero-duration", Group: "default", Type: model.LogTypeConsume, CreatedAt: now - 15, CompletionTokens: 9000, UseTime: 0},
	}
	logs = append(logs,
		model.Log{ChannelId: 8303, ModelName: "logged-platform-model", Group: "default", Type: model.LogTypeConsume, CreatedAt: now - 60, CompletionTokens: 100, UseTime: 2},
		model.Log{ChannelId: 8303, ModelName: "logged-platform-model", Group: "default", Type: model.LogTypeConsume, CreatedAt: now - 30, CompletionTokens: 200, UseTime: 4},
		model.Log{ChannelId: 8301, ModelName: "logged-platform-model", Group: "default", Type: model.LogTypeConsume, CreatedAt: now - 20, CompletionTokens: 9000, UseTime: 100},
		model.Log{ChannelId: 8303, ModelName: "logged-platform-model", Group: "vip", Type: model.LogTypeConsume, CreatedAt: now - 10, CompletionTokens: 9000, UseTime: 100},
		model.Log{ChannelId: 8303, ModelName: "logged-platform-model", Group: "default", Type: model.LogTypeConsume, CreatedAt: now - 90000, CompletionTokens: 9000, UseTime: 100},
		model.Log{ChannelId: 8303, ModelName: "logged-platform-model", Group: "default", Type: model.LogTypeError, CreatedAt: now - 5, CompletionTokens: 9000, UseTime: 100},
	)
	require.NoError(t, logDB.Create(&logs).Error)
	// Sharing now cannot authorize logs or health recorded while private.
	for range 3 {
		model.RecordChannelOutcome(8302, true, 321)
	}
	require.Positive(t, model.ChannelHealthOf(8302).Samples)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 8302).Update("visibility", 1).Error)
	for _, viewer := range []int{8301, 8302, 8301} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/public_pool/models", nil)
		ctx.Set("id", viewer)
		GetPublicPoolModels(ctx)
		var payload struct {
			Success bool `json:"success"`
			Data    struct {
				Items []struct {
					Model    string           `json:"model"`
					Channels []map[string]any `json:"channels"`
				} `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
		require.True(t, payload.Success, recorder.Body.String())
		assert.NotContains(t, recorder.Body.String(), "logged-secret")
		for _, item := range payload.Data.Items {
			require.Len(t, item.Channels, 1)
			channel := item.Channels[0]
			switch item.Model {
			case "logged-mixed":
				assert.Equal(t, float64(8302), channel["channel_id"])
				assert.Equal(t, "unknown", channel["status"])
				assert.Equal(t, "unknown", channel["health_state"])
				for _, field := range []string{"throughput", "tps", "latency_ms", "sample_count", "sampled_at", "response_time", "avg_latency_ms", "samples", "success_rate", "metric_source", "latency_source"} {
					assert.Nil(t, channel[field], "private history must not be disclosed after publication: %s", field)
				}
			case "logged-platform-model":
				assert.Equal(t, float64(8303), channel["channel_id"])
				assert.Equal(t, "channel_model", channel["metric_scope"])
				assert.Equal(t, "relay_logs_24h", channel["metric_source"])
				assert.Equal(t, "relay_logs_24h", channel["latency_source"])
				assert.Equal(t, "end_to_end", channel["tps_basis"])
				assert.Equal(t, float64(86400), channel["window_seconds"])
				assert.Equal(t, float64(2), channel["sample_count"])
				assert.InDelta(t, 2.0/1440, channel["throughput"], 1e-12)
				assert.Equal(t, float64(50), channel["tps"])
				assert.Equal(t, float64(3000), channel["latency_ms"])
				assert.Equal(t, float64(now-30), channel["sampled_at"])
			case "logged-unsampled":
				for _, field := range []string{"throughput", "tps", "latency_ms", "sample_count", "sampled_at"} {
					assert.Nil(t, channel[field], field)
				}
			case "logged-zero-duration":
				assert.Nil(t, channel["tps"])
				assert.Nil(t, channel["latency_ms"])
				assert.Equal(t, float64(1), channel["sample_count"])
			}
		}
	}
	briefs, err := model.ListPublicChannelsForGroups([]string{"default"})
	require.NoError(t, err)
	observed, err := model.GetPublicRelayLogMetrics(context.Background(), briefs, []string{"default"})
	require.NoError(t, err)
	assert.NotContains(t, observed, model.PublicChannelModelKey{ChannelID: 8302, ModelName: "logged-mixed"})
	assert.Contains(t, observed, model.PublicChannelModelKey{ChannelID: 8303, ModelName: "logged-platform-model"})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	metrics, err := model.GetPublicRelayLogMetrics(cancelled, briefs, []string{"default"})
	require.EqualError(t, err, "public relay metrics unavailable")
	assert.Empty(t, metrics)
	// A missing/unavailable separate log schema degrades cards to unknown,
	// never a 500 containing SQL, driver details, DSNs or private model names.
	model.LOG_DB = db
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/public_pool/models", nil)
	ctx.Set("id", 8301)
	GetPublicPoolModels(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	assert.Contains(t, recorder.Body.String(), `"throughput":null`)
	for _, secret := range []string{"SELECT", "no such table", "SQL", "logged-secret", "private"} {
		assert.NotContains(t, recorder.Body.String(), secret)
	}
}

func TestSetupLoginDoesNotTouchPasswordWhenPasswordFieldOmitted(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.AuditLog{}, &model.UserSession{}, &model.TwoFA{}, &model.PasskeyCredential{}))

	hashedPassword, err := common.Password2Hash("CurrentPassword123")
	require.NoError(t, err)
	user := &model.User{
		Username: "twofa-user",
		Password: hashedPassword,
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(user).Error)

	router := gin.New()
	router.GET("/", func(c *gin.Context) {
		setupLogin(&model.User{
			Id:          user.Id,
			AuthVersion: user.AuthVersion,
			Username:    user.Username,
			Role:        user.Role,
			Status:      user.Status,
			Group:       user.Group,
		}, nil, c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, hashedPassword, stored.Password)
}
