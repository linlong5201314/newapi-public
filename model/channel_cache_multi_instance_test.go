package model

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetryExcludesFailedChannelsForSameModel(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache=%t", cached), func(t *testing.T) {
			previousDB, previousCache := DB, common.MemoryCacheEnabled
			database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			DB = database
			common.MemoryCacheEnabled = cached
			t.Cleanup(func() {
				DB = previousDB
				common.MemoryCacheEnabled = previousCache
				ResetChannelHealthStoreForTest()
			})
			require.NoError(t, database.AutoMigrate(&Channel{}, &Ability{}))
			for _, id := range []int{701, 702, 703} {
				channel := &Channel{Id: id, OwnerId: 9101, Name: "same-model-retry", Type: 1, Key: "fixture-key", Models: "shared-model", Group: "default", Status: common.ChannelStatusEnabled}
				require.NoError(t, channel.Insert())
			}
			InitChannelCache()
			failed := map[int]bool{701: true, 702: true}
			selected, err := GetRandomSatisfiedChannel("default", "shared-model", 0, nil, 9101, failed)
			require.NoError(t, err)
			require.NotNil(t, selected)
			require.Equal(t, 703, selected.Id)
			selected, err = GetRandomSatisfiedChannel("default", "shared-model", 0, nil, 9101, map[int]bool{701: true, 702: true, 703: true})
			require.NoError(t, err)
			require.Nil(t, selected, "an exhausted model must not reuse a failed channel")
		})
	}
}

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
