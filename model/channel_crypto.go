// channel_crypto.go M5：账号型 2api 渠道凭据（channels.key）落库加密。
//
// 范围（按 M5 蓝图 §4）：只对 WorkBuddy(65) / Trae(66) 两类账号型渠道的新写入
// 凭据做 AES-GCM 加密；普通 API key 渠道维持上游 new-api 行为。读取点通过
// DecryptChannelKey 透明解密（历史明文无前缀值原样返回，实现无感迁移）。
package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// IsEncryptedChannelType 判断渠道类型是否使用加密凭据存储。
func IsEncryptedChannelType(channelType int) bool {
	switch channelType {
	case constant.ChannelTypeWorkBuddy, constant.ChannelTypeTrae:
		return true
	default:
		return false
	}
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

// DecryptChannelKey 透明解密账号型渠道凭据；历史明文/其他类型原样返回。
func DecryptChannelKey(channelType int, key string) string {
	if key == "" || !IsEncryptedChannelType(channelType) {
		return key
	}
	return common.DecryptSecretOrLegacy(key)
}

// EncryptCheckinCred 加密签到账号独立凭据（CheckinAccount.CredJson）。
func EncryptCheckinCred(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return common.EncryptSecretBestEffort(raw)
}

// DecryptCheckinCred 解密签到账号独立凭据；历史明文原样返回。
func DecryptCheckinCred(stored string) string {
	if stored == "" {
		return ""
	}
	return common.DecryptSecretOrLegacy(stored)
}
