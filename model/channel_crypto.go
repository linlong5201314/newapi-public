// channel_crypto.go encrypts every channel credential, including ordinary API
// keys and account credentials. Reads accept historical plaintext for a
// controlled migration; new writes use the versioned AES-GCM envelope.
package model

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

func EncryptChannelKeyStrict(channelType int, key string) (string, error) {
	if key == "" || common.IsEncryptedSecret(key) || common.IsPlainPrefixedSecret(key) {
		return key, nil
	}
	return common.EncryptSecret(key)
}

// IsEncryptedChannelType is retained for callers that distinguish legacy data.
// All newly written channel keys use the same versioned encrypted envelope.
func IsEncryptedChannelType(channelType int) bool {
	return true
}

// EncryptChannelKey 对账号型渠道凭据加密；其他类型/空值/已加密值原样返回（幂等）。
func EncryptChannelKey(channelType int, key string) string {
	if key == "" || !IsEncryptedChannelType(channelType) {
		return key
	}
	if common.IsEncryptedSecret(key) || common.IsPlainPrefixedSecret(key) {
		return key
	}
	return common.EncryptSecretBestEffort(key)
}

// DecryptChannelKey also recognizes explicitly encrypted keys on ordinary API
// channels (for example a server-owned account login gateway). Decryption errors
// fail closed: neither ciphertext nor a diagnostic containing it may be relayed.
func DecryptChannelKey(channelType int, key string) string {
	if key == "" || (!IsEncryptedChannelType(channelType) && !common.IsEncryptedSecret(key)) {
		return key
	}
	plain, err := common.DecryptSecret(key)
	if err != nil {
		common.SysError("Channel credential decryption failed; refusing to relay encrypted key")
		return ""
	}
	return plain
}

// EncryptCheckinCred 加密签到账号独立凭据（CheckinAccount.CredJson）。
func EncryptCheckinCred(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return common.EncryptSecretBestEffort(raw)
}

// MigrateChannelCredentialStorage upgrades known legacy plaintext envelopes in
// bounded batches. Current-version ciphertext and unreadable values are never overwritten.
func MigrateChannelCredentialStorage(batchSize int) (int, error) {
	if batchSize <= 0 || batchSize > 100 {
		batchSize = 50
	}
	migrated := 0
	for {
		var channels []Channel
		if err := DB.Select("id,type,key").Where("key <> '' AND key NOT LIKE ? AND key NOT LIKE ?", common.EncryptedSecretV2Prefix+"%", common.EncryptedSecretPrefix+"%").Order("id").Limit(batchSize).Find(&channels).Error; err != nil {
			return migrated, err
		}
		if len(channels) == 0 {
			return migrated, nil
		}
		for _, channel := range channels {
			raw := DecryptChannelKey(channel.Type, channel.Key)
			if raw == "" {
				return migrated, fmt.Errorf("channel credential migration refused for channel %d", channel.Id)
			}
			encrypted, err := EncryptChannelKeyStrict(channel.Type, raw)
			if err != nil || !common.IsEncryptedSecret(encrypted) {
				return migrated, fmt.Errorf("channel credential migration failed for channel %d", channel.Id)
			}
			result := DB.Model(&Channel{}).Where("id = ? AND key = ?", channel.Id, channel.Key).Update("key", encrypted)
			if result.Error != nil {
				return migrated, result.Error
			}
			if result.RowsAffected == 1 {
				migrated++
			}
		}
		if len(channels) < batchSize {
			return migrated, nil
		}
	}
}

// DecryptCheckinCred 解密签到账号独立凭据；历史明文原样返回。
func DecryptCheckinCred(stored string) string {
	if stored == "" {
		return ""
	}
	return common.DecryptSecretOrLegacy(stored)
}
