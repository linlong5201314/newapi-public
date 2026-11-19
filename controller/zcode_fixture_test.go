package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func zcodeControllerFixture(t *testing.T, dialect string) (*gorm.DB, model.AuthSessionIdentity, func(gin.HandlerFunc, string, string, string, model.AuthSessionIdentity) *httptest.ResponseRecorder, *http.Client) {
	var driver gorm.Dialector
	switch dialect {
	case "sqlite":
		driver = sqlite.Open(":memory:")
	case "mysql":
		dsn := os.Getenv("TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("TEST_MYSQL_DSN unavailable: real MySQL matrix blocked")
		}
		driver = mysql.Open(dsn)
	case "postgres":
		dsn := os.Getenv("TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("TEST_POSTGRES_DSN unavailable: real PostgreSQL matrix blocked")
		}
		driver = postgres.Open(dsn)
	}
	db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	oldDB, oldLog, oldType := model.DB, model.LOG_DB, common.MainDatabaseType()
	oldRedis, oldMemory := common.RedisEnabled, common.MemoryCacheEnabled
	oldCrypto, oldConfigured, oldSession := common.CryptoSecret, common.CryptoSecretConfigured, common.SessionSecret
	oldEnabled, oldLimit := operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseType(dialect))
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.CryptoSecret, common.CryptoSecretConfigured, common.SessionSecret = "zcode-account-test-key", true, "zcode-account-test-session"
	operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = true, 30
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		common.SetMainDatabaseType(oldType)
		common.RedisEnabled, common.MemoryCacheEnabled = oldRedis, oldMemory
		common.CryptoSecret, common.CryptoSecretConfigured, common.SessionSecret = oldCrypto, oldConfigured, oldSession
		operation_setting.SelfChannelEnabled, operation_setting.SelfChannelMaxPerUser = oldEnabled, oldLimit
		assert.NoError(t, sqlDB.Close())
	})
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.Channel{}, &model.Ability{}, &model.Option{}))
	}
	versionSQL := "SELECT version()"
	if dialect == "sqlite" {
		versionSQL = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
	t.Logf("%s version %s", dialect, version)
	name := "zcode-" + uuid.NewString()[:8]
	user := model.User{Username: name, AffCode: name, Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AuthVersion: 1, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	platform := model.UserSession{SID: uuid.NewString(), UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: uuid.NewString(), LoginMethod: "password"}
	require.NoError(t, db.Create(&platform).Error)
	identity := model.AuthSessionIdentity{UserID: user.Id, SessionID: platform.SID, UserAuthVersion: 1, SessionVersion: 1}
	t.Cleanup(func() {
		var channels []model.Channel
		require.NoError(t, db.Where("owner_id = ?", user.Id).Find(&channels).Error)
		for _, ch := range channels {
			assert.NoError(t, db.Where("channel_id = ?", ch.Id).Delete(&model.Ability{}).Error)
		}
		assert.NoError(t, db.Where("owner_id = ?", user.Id).Delete(&model.Channel{}).Error)
		assert.NoError(t, db.Where("user_id = ?", user.Id).Delete(&model.AuthFlow{}).Error)
		assert.NoError(t, db.Where("user_id = ?", user.Id).Delete(&model.UserSession{}).Error)
		assert.NoError(t, db.Unscoped().Delete(&user).Error)
	})
	request := func(handler gin.HandlerFunc, method, body, origin string, id model.AuthSessionIdentity) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", id.UserID)
		c.Set("session_id", id.SessionID)
		c.Set("auth_version", id.UserAuthVersion)
		c.Set("session_version", id.SessionVersion)
		c.Request = httptest.NewRequest(method, "https://dashboard.test/api/channel/self/account", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("Origin", origin)
		handler(c)
		return recorder
	}
	service.InitHttpClient()
	client := service.GetHttpClient()
	oldClient := *client
	t.Cleanup(func() { *client = oldClient })

	return db, identity, request, client
}
