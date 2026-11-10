package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupNodeModelTestDB(t *testing.T) {
	t.Helper()
	previousDB := DB
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := database.AutoMigrate(&Node{}, &NodeAudit{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	DB = database
	t.Cleanup(func() { DB = previousDB })
}

func withTestCryptoSecret(t *testing.T) {
	t.Helper()
	previousSecret, previousConfigured := common.CryptoSecret, common.CryptoSecretConfigured
	common.CryptoSecret = "node-model-test-secret"
	common.CryptoSecretConfigured = true
	t.Cleanup(func() {
		common.CryptoSecret, common.CryptoSecretConfigured = previousSecret, previousConfigured
	})
}

func TestNodeApiTokenEncryptedAtRestAndReadable(t *testing.T) {
	withTestCryptoSecret(t)
	setupNodeModelTestDB(t)

	node := &Node{Name: "e2e", Provider: "railway", Enabled: true, ApiTokenEnc: "railway-token-plain"}
	if err := CreateNode(node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	var stored string
	if err := DB.Model(&Node{}).Where("id = ?", node.Id).Pluck("api_token_enc", &stored).Error; err != nil {
		t.Fatalf("read raw token: %v", err)
	}
	if !strings.HasPrefix(stored, common.EncryptedSecretPrefix) {
		t.Fatalf("expected encrypted token at rest, got %q", stored)
	}
	if strings.Contains(stored, "railway-token-plain") {
		t.Fatal("ciphertext must not contain the plaintext token")
	}
	loaded, err := GetNodeById(node.Id)
	if err != nil {
		t.Fatalf("load node: %v", err)
	}
	if loaded.DecryptedToken() != "railway-token-plain" {
		t.Fatalf("decrypted token mismatch: %q", loaded.DecryptedToken())
	}
	if !loaded.HasApiToken() {
		t.Fatal("HasApiToken must report true")
	}
}

func TestChannelCredentialEncryptionHelpers(t *testing.T) {
	withTestCryptoSecret(t)
	raw := `{"accessToken":"at","refreshToken":"rt"}`
	encrypted := EncryptChannelKey(constant.ChannelTypeWorkBuddy, raw)
	if !common.IsEncryptedSecret(encrypted) {
		t.Fatalf("expected encrypted account-channel key, got %q", encrypted)
	}
	if DecryptChannelKey(constant.ChannelTypeWorkBuddy, encrypted) != raw {
		t.Fatal("channel key roundtrip failed")
	}
	// 幂等：重复写入不能二次加密。
	if again := EncryptChannelKey(constant.ChannelTypeWorkBuddy, encrypted); again != encrypted {
		t.Fatal("EncryptChannelKey must be idempotent")
	}
	// 普通 API key 渠道保持上游行为（不加密）。
	if plain := EncryptChannelKey(constant.ChannelTypeOpenAI, "sk-test"); plain != "sk-test" {
		t.Fatalf("non-account channel must stay plaintext, got %q", plain)
	}
	channel := &Channel{Type: constant.ChannelTypeTrae, Key: encrypted}
	keys := channel.GetKeys()
	if len(keys) != 1 || keys[0] != raw {
		t.Fatalf("GetKeys must transparently decrypt, got %#v", keys)
	}
	// 历史明文（无前缀）原样返回。
	legacyChannel := &Channel{Type: constant.ChannelTypeTrae, Key: raw}
	if key, _, apiErr := legacyChannel.GetNextEnabledKey(); apiErr != nil || key != raw {
		t.Fatalf("legacy plaintext key must pass through, got %q err=%v", key, apiErr)
	}
}

func TestCheckinCredentialEncryptionHelpers(t *testing.T) {
	withTestCryptoSecret(t)
	plain := `{"cookie":"session=abc"}`
	encrypted := EncryptCheckinCred(plain)
	if !common.IsEncryptedSecret(encrypted) {
		t.Fatalf("expected encrypted checkin cred, got %q", encrypted)
	}
	if DecryptCheckinCred(encrypted) != plain {
		t.Fatal("checkin cred roundtrip failed")
	}
	if DecryptCheckinCred(plain) != plain {
		t.Fatal("legacy plaintext checkin cred must pass through")
	}
	if EncryptCheckinCred("") != "" {
		t.Fatal("empty cred must stay empty")
	}
}
