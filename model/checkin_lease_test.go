package model

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCheckinLeaseFencesStaleResults(t *testing.T) {
	previousDB := DB
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	DB = database
	t.Cleanup(func() { DB = previousDB })
	require.NoError(t, database.AutoMigrate(&CheckinAccount{}, &CheckinLog{}))
	account := &CheckinAccount{UserId: 7001, Site: "custom", Enabled: true}
	require.NoError(t, DB.Create(account).Error)
	now := time.Now()
	require.NoError(t, ClaimCheckinAccount(account, "lease-a", now, time.Minute))
	assert.NoError(t, ClaimCheckinAccount(account, "lease-a", now, time.Minute), "repeated claim for the same owner is idempotent")
	var afterFirst CheckinAccount
	require.NoError(t, DB.First(&afterFirst, account.Id).Error)
	t.Logf("first lease: until=%d token=%q now=%d", afterFirst.RunLeaseUntil, afterFirst.RunLeaseToken, now.Unix())
	assert.ErrorIs(t, ClaimCheckinAccount(account, "lease-b", now, time.Minute), ErrCheckinLeaseLost)
	require.NoError(t, RenewCheckinAccountLease(account, now.Add(30*time.Second), 2*time.Minute))
	// Simulate expiry followed by another node's independent account object.
	otherNode := &CheckinAccount{Id: account.Id, UserId: account.UserId, Site: account.Site}
	require.NoError(t, DB.Model(&CheckinAccount{}).Where("id = ?", account.Id).Update("run_lease_until", now.Unix()-1).Error)
	require.NoError(t, ClaimCheckinAccount(otherNode, "lease-b", now, time.Minute))
	assert.ErrorIs(t, ClaimCheckinAccount(account, "stale-lease", now, time.Minute), ErrCheckinLeaseLost)
	account.LastStatus = "ok"
	account.LastResult = "stale result"
	assert.ErrorIs(t, SaveCheckinRunResult(account, "stale", ""), ErrCheckinLeaseLost)
	var count int64
	require.NoError(t, DB.Model(&CheckinLog{}).Count(&count).Error)
	assert.Zero(t, count)
	var stored CheckinAccount
	require.NoError(t, DB.First(&stored, account.Id).Error)
	assert.Equal(t, "lease-b", stored.RunLeaseToken)
	assert.NotEqual(t, "ok", stored.LastStatus)
}

func TestCheckinRetryAfterBlocksSweep(t *testing.T) {
	previousDB := DB
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	DB = database
	t.Cleanup(func() { DB = previousDB })
	require.NoError(t, database.AutoMigrate(&CheckinAccount{}, &CheckinLog{}))
	account := &CheckinAccount{UserId: 7002, Site: "custom"}
	require.NoError(t, DB.Create(account).Error)
	require.NoError(t, ClaimCheckinAccount(account, "lease", time.Now(), time.Minute))
	account.LastStatus, account.LastCheckinDay = "failed", time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	account.FailCount, account.RetryAfterUntil = 1, time.Now().Add(time.Hour).Unix()
	require.NoError(t, SaveCheckinRunResult(account, "retry later", ""))
	var stored CheckinAccount
	require.NoError(t, DB.First(&stored, account.Id).Error)
	assert.Equal(t, account.RetryAfterUntil, stored.RetryAfterUntil)
	assert.Empty(t, stored.RunLeaseToken)
	var log CheckinLog
	require.NoError(t, DB.First(&log).Error)
	assert.Equal(t, "retry later", log.Detail)
}
