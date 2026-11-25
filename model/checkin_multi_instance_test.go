package model

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCheckinLeaseMutualExclusionAcrossInstances(t *testing.T) {
	previousDB := DB
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "shared-checkin.db")), &gorm.Config{})
	require.NoError(t, err)
	DB = database
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	t.Cleanup(func() { DB = previousDB })
	require.NoError(t, database.AutoMigrate(&CheckinAccount{}, &CheckinLog{}))
	account := &CheckinAccount{UserId: 9001, Site: "custom", Enabled: true}
	require.NoError(t, database.Create(account).Error)
	first := &CheckinAccount{Id: account.Id}
	second := &CheckinAccount{Id: account.Id}
	now := time.Now()
	require.NoError(t, ClaimCheckinAccount(first, "node-a", now, time.Minute))
	require.ErrorIs(t, ClaimCheckinAccount(second, "node-b", now, time.Minute), ErrCheckinLeaseLost)
	require.NoError(t, RenewCheckinAccountLease(first, now.Add(time.Second), time.Minute))
	first.LastStatus = "ok"
	first.LastResult = "completed on node-a"
	require.NoError(t, SaveCheckinRunResult(first, "node-a", ""))
	second.RunLeaseToken = "node-b"
	second.LastStatus = "failed"
	require.ErrorIs(t, SaveCheckinRunResult(second, "stale node-b", ""), ErrCheckinLeaseLost)
	var stored CheckinAccount
	require.NoError(t, database.First(&stored, account.Id).Error)
	require.Equal(t, "ok", stored.LastStatus)
	var count int64
	require.NoError(t, database.Model(&CheckinLog{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
