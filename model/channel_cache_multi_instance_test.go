package model

import (
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelSnapshotSharedAcrossInstances(t *testing.T) {
	previousDB, previousCache := DB, common.MemoryCacheEnabled
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "shared-channels.db")), &gorm.Config{})
	require.NoError(t, err)
	DB = database
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = previousCache
		DB = previousDB
		ResetChannelHealthStoreForTest()
	})
	require.NoError(t, database.AutoMigrate(&Channel{}, &Ability{}))
	channel := &Channel{OwnerId: 9101, Name: "shared-node-fixture", Type: 1, Key: "fixture-key", Models: "shared-model", Group: "default", Status: common.ChannelStatusEnabled}
	require.NoError(t, channel.Insert())
	for instance := 0; instance < 2; instance++ {
		InitChannelCache()
		selected, err := GetRandomSatisfiedChannel("default", "shared-model", 0, nil, 9101)
		require.NoError(t, err)
		require.NotNil(t, selected)
		require.Equal(t, channel.Id, selected.Id)
	}
}
