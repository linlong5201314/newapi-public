package model

import (
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm/logger"
	"os"
	"strings"
	"testing"

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
					}
				})
			}
		})
	}
}
