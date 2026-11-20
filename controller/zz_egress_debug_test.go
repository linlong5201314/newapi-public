package controller

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestDebugEgress(t *testing.T) {
	previousDB := model.DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousCache, previousRedis, previousSQLite := common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousDB
		common.SetDatabaseTypes(previousType, previousLogType)
		common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath = previousMaster, previousCache, previousRedis, previousSQLite
	})
	t.Setenv("SQL_DSN", "")
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled = false, false, false
	common.SQLitePath = filepath.Join(t.TempDir(), "dbg.db")
	require.NoError(t, model.InitDB())
	sqlDB, _ := model.DB.DB()
	t.Cleanup(func() { sqlDB.Close() })
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Option{}))
	operation_setting.SelfChannelEnabled = true
	user := &model.User{Username: "dbg", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(user).Error)
	settings := dto.ChannelSettings{Proxy: "", ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "user_pool"}}
	proxy, err := model.ResolveStableChannelProxy(user.Id, 0, "cline:global:abc", settings)
	fmt.Fprintf(os.Stderr, "user_pool: proxy=%q err=%v\n", proxy, err)
	settings2 := dto.ChannelSettings{Proxy: "", ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "direct"}}
	proxy, err = model.ResolveStableChannelProxy(user.Id, 0, "cline:global:abc", settings2)
	fmt.Fprintf(os.Stderr, "direct: proxy=%q err=%v\n", proxy, err)
	time.Sleep(10 * time.Millisecond)
}
