package model

import (
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm/logger"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelProxyPolicyVisibilityAndDeletion(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	old, oldReady := DB, proxySchemaReadyDB
	DB, proxySchemaReadyDB = db, nil
	t.Cleanup(func() { DB, proxySchemaReadyDB = old, oldReady; _ = sqlDB.Close() })
	require.NoError(t, EnsureProxyPoolSchema())
	private := ProxyEndpoint{UserId: 1, Name: "private", Kind: "http", Url: "http://127.0.0.1:1111", Enabled: true}
	public := ProxyEndpoint{UserId: 2, Name: "public", Kind: "http", Url: "http://127.0.0.1:2222", Public: true, Enabled: true}
	require.NoError(t, db.Create(&private).Error)
	require.NoError(t, db.Create(&public).Error)
	policy := &dto.ChannelProxyPolicy{Mode: "endpoint", EndpointID: private.Id}
	require.Error(t, ValidateSelfChannelProxyPolicy(2, policy, ""))
	got, err := ResolveChannelProxyPolicy(1, dto.ChannelSettings{ProxyPolicy: policy})
	require.NoError(t, err)
	assert.Equal(t, private.Url, got)
	got, err = ResolveChannelProxyPolicy(1, dto.ChannelSettings{ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "public_pool"}})
	require.NoError(t, err)
	assert.Equal(t, public.Url, got)
	got, err = ResolveChannelProxyPolicy(1, dto.ChannelSettings{ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "user_pool"}})
	require.NoError(t, err)
	assert.Equal(t, private.Url, got)
	require.NoError(t, db.Delete(&private).Error)
	got, err = ResolveChannelProxyPolicy(1, dto.ChannelSettings{ProxyPolicy: policy})
	require.Error(t, err)
	assert.Empty(t, got)
	got, err = ResolveChannelProxyPolicy(1, dto.ChannelSettings{Proxy: "direct"})
	require.NoError(t, err)
	assert.Empty(t, got)
	got, err = ResolveChannelProxyPolicy(1, dto.ChannelSettings{Proxy: public.Url})
	require.NoError(t, err)
	assert.Equal(t, public.Url, got)
	require.Error(t, ValidateSelfChannelProxyPolicy(1, &dto.ChannelProxyPolicy{Mode: "fixed"}, "direct"))
	require.Error(t, ValidateSelfChannelProxyPolicy(1, &dto.ChannelProxyPolicy{Mode: "direct", EndpointID: 1}, ""))
}

func TestStableProxyAccountLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	oldDB, oldReady := DB, proxySchemaReadyDB
	DB, proxySchemaReadyDB = db, nil
	t.Cleanup(func() { DB, proxySchemaReadyDB = oldDB, oldReady; _ = sqlDB.Close() })
	require.NoError(t, EnsureProxyPoolSchema())
	require.NoError(t, db.Exec("CREATE TABLE channels (id integer primary key, owner_id integer)").Error)
	require.NoError(t, db.Exec("INSERT INTO channels (id,owner_id) VALUES (801,1)").Error)
	rows := []ProxyEndpoint{
		{UserId: 9, Name: "HK public", Region: "HK", Kind: "http", Url: "http://127.0.0.1:18801", Public: true, Enabled: true},
		{UserId: 9, Name: "TW public", Region: "TW", Kind: "http", Url: "http://127.0.0.1:18802", Public: true, Enabled: true},
		{UserId: 2, Name: "other private", Kind: "http", Url: "http://127.0.0.1:18803", Enabled: true},
	}
	require.NoError(t, db.Create(&rows).Error)
	setting := dto.ChannelSettings{ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "public_pool"}}
	account := "workbuddy:opaque-test-flow"
	before, err := ResolveStableChannelProxy(1, 0, account, setting)
	require.NoError(t, err)
	require.Contains(t, []string{rows[0].Url, rows[1].Url}, before)
	require.NoError(t, BindChannelProxyAccount(1, 801, account))
	require.NoError(t, BindChannelProxyAccount(1, 801, account))
	require.Error(t, BindChannelProxyAccount(2, 801, account))
	require.Error(t, BindChannelProxyAccount(1, 801, "workbuddy:other"))
	after, err := ResolveStableChannelProxy(1, 801, "", setting)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	now := time.Now()
	healthyUS := ProxyEndpoint{UserId: 9, Name: "US healthy", Region: "US", Kind: "http", Url: "http://127.0.0.1:18804", Public: true, Enabled: true, LastOkAt: &now}
	require.NoError(t, db.Create(&healthyUS).Error)
	preferred, err := ResolveStableChannelProxy(1, 0, "trae:opaque-region-flow", setting)
	require.NoError(t, err)
	assert.Contains(t, []string{rows[0].Url, rows[1].Url}, preferred, "HK/TW preferred over healthy US for domestic providers")
	for range 3 {
		repeated, err := ResolveStableChannelProxy(1, 801, "", setting)
		require.NoError(t, err)
		assert.Equal(t, before, repeated, "a healthy account must not rotate")
	}
	require.True(t, InvalidateStableProxyURL(after))
	next, err := ResolveStableChannelProxy(1, 801, "", setting)
	require.NoError(t, err)
	assert.NotEqual(t, after, next)
	require.NoError(t, db.Model(&ProxyEndpoint{}).Where("public = ?", true).Update("enabled", false).Error)
	got, err := ResolveStableChannelProxy(1, 801, "", setting)
	require.Error(t, err)
	assert.Empty(t, got)
	got, err = ResolveStableChannelProxy(0, 0, "internal", setting)
	require.Error(t, err)
	assert.Empty(t, got)
}

func TestProxyStickyRedisAtomicBindingAndOutage(t *testing.T) {
	server := miniredis.RunT(t)
	options := redis.Options{Addr: server.Addr(), DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second}
	client := redis.NewClient(&options)
	other := redis.NewClient(&options)
	old, oldEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() { common.RDB, common.RedisEnabled = old, oldEnabled; _ = client.Close(); _ = other.Close() })
	// Complete the connection handshake first so failures identify startup rather
	// than being hidden by the production store's deliberately redacted error.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err(), "first fixture Redis connection")
	require.NoError(t, other.Ping(ctx).Err(), "second fixture Redis connection")
	key := "proxy-pool:sticky:v1:owner:1:test"
	empty := ""
	value, err := proxyStickyValue(key, &empty, "11")
	require.NoError(t, err, "first atomic binding after Redis PING readiness")
	assert.Equal(t, "11", value)
	common.RDB = other
	value, err = proxyStickyValue(key, &empty, "22")
	require.NoError(t, err)
	assert.Equal(t, "11", value, "second instance adopts the existing winner")
	common.RDB = client
	server.FastForward(29 * 24 * time.Hour)
	value, err = proxyStickyValue(key, nil, "")
	require.NoError(t, err)
	assert.Equal(t, "11", value)
	assert.Greater(t, server.TTL(key), 29*24*time.Hour)
	server.Close()
	// Only the deliberate outage uses short I/O deadlines and disables retries.
	// Exercise the production resolver, not just a raw Redis error.
	outage := redis.NewClient(&redis.Options{Addr: options.Addr, MaxRetries: -1, DialTimeout: 250 * time.Millisecond, ReadTimeout: 250 * time.Millisecond, WriteTimeout: 250 * time.Millisecond,
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			defer cancel()
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	})
	t.Cleanup(func() { _ = outage.Close() })
	common.RDB = outage
	_, err = proxyStickyValue(key, &empty, "33")
	require.Error(t, err, "Redis outage must not select a divergent local endpoint")
	common.RDB = nil
	t.Setenv("REDIS_CONN_STRING", "redis://127.0.0.1:1")
	_, err = proxyStickyValue(key, nil, "")
	require.Error(t, err)
}

func TestProxyPoolStatsSeparateInventoryEnabledAndProbed(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	old, oldReady := DB, proxySchemaReadyDB
	DB, proxySchemaReadyDB = db, nil
	t.Cleanup(func() { DB, proxySchemaReadyDB = old, oldReady; _ = sqlDB.Close() })
	require.NoError(t, EnsureProxyPoolSchema())
	verifyProxyPoolStats(t, db)
}

func verifyProxyPoolStats(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now()
	rows := []ProxyEndpoint{
		{UserId: 5, Name: "healthy", Kind: "http", Enabled: true, LastOkAt: &now},
		{UserId: 5, Name: "unknown", Kind: "http", Enabled: true},
		{UserId: 9, Name: "failed public", Kind: "http", Public: true, Enabled: true, FailCount: 1, LastOkAt: &now},
		{UserId: 5, Name: "disabled", Kind: "http", Enabled: true},
		{UserId: 2, Name: "other private", Kind: "http", Enabled: true},
	}
	require.NoError(t, db.Create(&rows).Error)
	require.NoError(t, db.Model(&ProxyEndpoint{}).Where("id = ?", rows[3].Id).Update("enabled", false).Error)
	stats, err := GetProxyPoolStatsForUser(5)
	require.NoError(t, err)
	assert.Equal(t, 4, stats.Total)
	assert.Equal(t, 3, stats.Enabled)
	assert.Equal(t, 1, stats.HealthyNodes)
	assert.Equal(t, 1, stats.UnknownNodes)
	assert.Equal(t, 1, stats.FailedNodes)
	assert.Equal(t, stats.Enabled, stats.HealthyNodes+stats.UnknownNodes+stats.FailedNodes)
	require.NoError(t, db.Where("id IN ?", []int{rows[0].Id, rows[1].Id, rows[2].Id, rows[3].Id, rows[4].Id}).Delete(&ProxyEndpoint{}).Error)
}

func TestEnsureProxyPoolSchemaRetriesAfterFailure(t *testing.T) {
	old, oldReady := DB, proxySchemaReadyDB
	t.Cleanup(func() { DB = old; proxySchemaReadyDB = oldReady })
	DB, proxySchemaReadyDB = nil, nil
	require.Error(t, EnsureProxyPoolSchema())
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	DB = db
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	require.Error(t, EnsureProxyPoolSchema())
	require.NoError(t, db.Exec("PRAGMA query_only = OFF").Error)
	require.NoError(t, EnsureProxyPoolSchema())
	assert.True(t, db.Migrator().HasColumn(&ProxyEndpoint{}, "LatencyMs"))
}

func TestEnsureProxyPoolSchema(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "fresh"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			old := DB
			oldReady := proxySchemaReadyDB
			DB = db
			proxySchemaReadyDB = nil
			t.Cleanup(func() { DB = old; proxySchemaReadyDB = oldReady; _ = sqlDB.Close() })
			if legacy {
				require.NoError(t, db.Exec("CREATE TABLE proxy_endpoints (id integer primary key, user_id integer, name text)").Error)
				require.NoError(t, db.Exec("INSERT INTO proxy_endpoints (id, user_id, name) VALUES (7, 2, 'retained')").Error)
				require.NoError(t, db.Exec("CREATE TABLE proxy_subscriptions (id integer primary key)").Error)
			}
			for range 2 {
				// Clear the process cache to simulate another startup.
				proxySchemaReadyDB = nil
				require.NoError(t, EnsureProxyPoolSchema())
				for _, field := range []string{"Public", "LatencyMs", "Source"} {
					assert.True(t, db.Migrator().HasColumn(&ProxyEndpoint{}, field), field)
				}
				assert.True(t, db.Migrator().HasColumn(&ProxySubscription{}, "Public"))
				for _, target := range []any{&ProxyEndpoint{}, &ProxySubscription{}} {
					stmt := &gorm.Statement{DB: db}
					require.NoError(t, stmt.Parse(target))
					for _, field := range stmt.Schema.Fields {
						assert.True(t, db.Migrator().HasColumn(target, field.DBName), field.DBName)
					}
					for name := range stmt.Schema.ParseIndexes() {
						assert.True(t, db.Migrator().HasIndex(target, name), name)
					}
				}
				_, err = ListProxyEndpoints(2)
				require.NoError(t, err)
				_, err = ListAllEnabledProxyEndpoints()
				require.NoError(t, err)
				_, err = GetProxyPoolStatsForUser(2)
				require.NoError(t, err)
				_, err = ListProxySubscriptions(2)
				require.NoError(t, err)
			}
			if legacy {
				var name string
				require.NoError(t, db.Table("proxy_endpoints").Select("name").Where("id = ?", 7).Scan(&name).Error)
				assert.Equal(t, "retained", name)
			}
		})
	}
}

// TestProxyPoolSchemaExternalDatabases requires an empty disposable loopback DB.
// It never reads SQL_DSN or application configuration.
func TestProxyPoolSchemaExternalDatabases(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			key := "TEST_MYSQL_DSN"
			if dialect == "postgres" {
				key = "TEST_POSTGRES_DSN"
			}
			dsn := os.Getenv(key)
			if dsn == "" {
				t.Skip(key + " not provided; real database matrix BLOCKED")
			}
			require.Equal(t, "1", os.Getenv("TEST_PROXY_POOL_DISPOSABLE"), "explicit disposable database opt-in required")
			require.True(t, strings.Contains(dsn, "127.0.0.1") || strings.Contains(dsn, "localhost"), "only loopback test databases are allowed")
			var driver gorm.Dialector = mysql.Open(dsn)
			if dialect == "postgres" {
				driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.True(t, err == nil, "cannot open dedicated test database; details suppressed to protect credentials")
			sqlDB, err := db.DB()
			require.NoError(t, err)
			tables, err := db.Migrator().GetTables()
			require.NoError(t, err)
			require.Empty(t, tables, "refuse to modify a nonempty database; use a new disposable database")
			var version string
			if dialect == "postgres" {
				require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
			} else {
				require.NoError(t, db.Raw("SELECT VERSION()").Scan(&version).Error)
			}
			t.Log("database version: " + version)
			old, oldReady := DB, proxySchemaReadyDB
			DB, proxySchemaReadyDB = db, nil
			t.Cleanup(func() {
				_ = db.Migrator().DropTable(&ProxyEndpoint{}, &ProxySubscription{})
				_ = sqlDB.Close()
				DB, proxySchemaReadyDB = old, oldReady
			})
			for _, legacy := range []bool{false, true} {
				name := "fresh"
				if legacy {
					name = "legacy"
				}
				t.Run(name, func(t *testing.T) {
					require.NoError(t, db.Migrator().DropTable(&ProxyEndpoint{}, &ProxySubscription{}))
					if legacy {
						require.NoError(t, db.Exec("CREATE TABLE proxy_endpoints (id bigint primary key, user_id bigint, name varchar(120))").Error)
						require.NoError(t, db.Exec("INSERT INTO proxy_endpoints (id,user_id,name) VALUES (7,2,'retained')").Error)
						require.NoError(t, db.Exec("CREATE TABLE proxy_subscriptions (id bigint primary key)").Error)
					}
					for range 2 {
						proxySchemaReadyDB = nil
						require.NoError(t, EnsureProxyPoolSchema())
						for _, target := range []any{&ProxyEndpoint{}, &ProxySubscription{}} {
							stmt := &gorm.Statement{DB: db}
							require.NoError(t, stmt.Parse(target))
							for _, field := range stmt.Schema.Fields {
								assert.True(t, db.Migrator().HasColumn(target, field.DBName), field.DBName)
							}
							for name := range stmt.Schema.ParseIndexes() {
								assert.True(t, db.Migrator().HasIndex(target, name), name)
							}
						}
						_, err = ListProxyEndpoints(2)
						require.NoError(t, err)
						_, err = ListAllEnabledProxyEndpoints()
						require.NoError(t, err)
						_, err = GetProxyPoolStatsForUser(2)
						require.NoError(t, err)
						_, err = ListProxySubscriptions(2)
						require.NoError(t, err)
					}
					if legacy {
						var row ProxyEndpoint
						require.NoError(t, db.First(&row, 7).Error)
						assert.Equal(t, "retained", row.Name)
						assert.Equal(t, 2, row.UserId)
					} else {
						row := ProxyEndpoint{UserId: 2, Name: "created", Kind: "http", Enabled: true}
						require.NoError(t, db.Create(&row).Error)
						assert.Positive(t, row.Id)
						require.NoError(t, db.Delete(&row).Error)
						verifyProxyPoolStats(t, db)
					}
				})
			}
		})
	}
}

func TestStableProxyEndpointIdentitySurvivesListenerRelocation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	oldDB, oldReady, oldProvider, oldRedis := DB, proxySchemaReadyDB, endpointProxyURLProvider, common.RedisEnabled
	DB, proxySchemaReadyDB, common.RedisEnabled = db, nil, false
	t.Cleanup(func() {
		DB, proxySchemaReadyDB, endpointProxyURLProvider, common.RedisEnabled = oldDB, oldReady, oldProvider, oldRedis
		_ = sqlDB.Close()
	})
	require.NoError(t, EnsureProxyPoolSchema())
	endpoint := ProxyEndpoint{UserId: 9, Name: "runtime endpoint", Kind: "vless", RawUri: "fixture-endpoint-config", Public: true, Enabled: true}
	require.NoError(t, db.Create(&endpoint).Error)
	current := "http://endpoint:test@127.0.0.1:24001"
	endpointProxyURLProvider = func(ProxyEndpoint) (string, error) { return current, nil }
	settings := dto.ChannelSettings{ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "endpoint", EndpointID: endpoint.Id}}
	first, err := ResolveStableChannelProxy(1, 0, "workbuddy:identity-regression", settings)
	require.NoError(t, err)
	id, fingerprint, err := ResolveProxyEndpointIdentity(1, first)
	require.NoError(t, err)
	current = "http://endpoint:test@127.0.0.1:24002"
	next, err := ResolveStableChannelProxy(1, 0, "workbuddy:identity-regression", settings)
	require.NoError(t, err)
	nextID, nextFingerprint, err := ResolveProxyEndpointIdentity(1, next)
	require.NoError(t, err)
	assert.NotEqual(t, first, next)
	assert.Equal(t, id, nextID)
	assert.Equal(t, fingerprint, nextFingerprint)
	_, _, err = ResolveProxyEndpointIdentity(1, first)
	require.Error(t, err, "stale listener URL is not reusable")
	require.NoError(t, db.Model(&endpoint).Update("raw_uri", "changed-real-exit").Error)
	_, _, err = ResolveProxyEndpointIdentity(1, next)
	require.Error(t, err, "real endpoint configuration change must invalidate the fingerprint")
}
