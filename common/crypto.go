package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------------
// M5 秘密字段加密（AES-256-GCM，CRYPTO_SECRET 派生）
//
// 存储格式：
//
//	enc:v1:<base64url(nonce||ciphertext||tag)>
//	plain:<原文>            CRYPTO_SECRET 未配置时的降级存储，启动期 WARN
//
// 无前缀的旧值按历史明文处理（DecryptSecret 原样返回），从而兼容 M1-M4
// 已落库的数据；再次写入时自动升级为密文。
// ---------------------------------------------------------------------------

const (
	// EncryptedSecretPrefix 已加密值的存储前缀（版本化，便于未来轮换算法）。
	EncryptedSecretPrefix = "enc:v1:"
	// PlainSecretPrefix 未配置 CRYPTO_SECRET 时的显式明文前缀。
	PlainSecretPrefix = "plain:"

	secretDerivationLabel = "new-api:crypto:v1:"
)

var (
	// CryptoSecretConfigured 表示 CRYPTO_SECRET 是否由环境显式提供。
	// 未配置时加密字段以 plain: 前缀存储并标记迁移（启动 WARN）。
	CryptoSecretConfigured bool

	errSecretKeyUnavailable = errors.New("CRYPTO_SECRET is not configured")
)

var (
	secretAEAD     cipher.AEAD
	secretAEADErr  error
	secretAEADSeed string
	secretAEADMu   sync.Mutex
)

// secretAEADFor 返回当前 CryptoSecret 派生的 AES-GCM AEAD。密钥变更（测试/热配置）
// 时重新派生，生产环境 CryptoSecret 启动后不变。
func secretAEADFor() (cipher.AEAD, error) {
	secretAEADMu.Lock()
	defer secretAEADMu.Unlock()
	if !CryptoSecretConfigured || strings.TrimSpace(CryptoSecret) == "" {
		return nil, errSecretKeyUnavailable
	}
	if secretAEAD != nil && secretAEADSeed == CryptoSecret {
		return secretAEAD, secretAEADErr
	}
	secretAEADSeed = CryptoSecret
	secretAEAD = nil
	secretAEADErr = nil
	// SHA-256 派生定长 32 字节 key，避免直接使用任意长度口令。
	digest := sha256.Sum256([]byte(secretDerivationLabel + CryptoSecret))
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		secretAEADErr = err
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		secretAEADErr = err
		return nil, err
	}
	secretAEAD = aead
	return aead, nil
}

// IsEncryptedSecret 判断存储值是否已是密文（enc:v1: 前缀）。
func IsEncryptedSecret(value string) bool {
	return strings.HasPrefix(value, EncryptedSecretPrefix)
}

// IsPlainPrefixedSecret 判断存储值是否显式标记为明文（plain: 前缀）。
func IsPlainPrefixedSecret(value string) bool {
	return strings.HasPrefix(value, PlainSecretPrefix)
}

// EncryptSecret 加密敏感字段。CRYPTO_SECRET 未配置时返回 plain:<原文>（nil error），
// 由启动 WARN 提示运维补齐并迁移；配置后返回 enc:v1:<base64url>。
func EncryptSecret(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	aead, err := secretAEADFor()
	if err != nil {
		if errors.Is(err, errSecretKeyUnavailable) {
			return PlainSecretPrefix + plaintext, nil
		}
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, nonce, []byte(plaintext), nil)
	payload := make([]byte, 0, len(nonce)+len(sealed))
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	return EncryptedSecretPrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

// EncryptSecretBestEffort 与 EncryptSecret 相同，但把随机源/配置异常降级为
// plain: 前缀并记录日志——写库路径不因加密子系统故障丢失业务数据。
func EncryptSecretBestEffort(plaintext string) string {
	encrypted, err := EncryptSecret(plaintext)
	if err != nil {
		SysError(LogText("secret encryption failed, storing plain-prefixed value: %s", err.Error()))
		return PlainSecretPrefix + plaintext
	}
	return encrypted
}

// DecryptSecret 解密存储值。规则：
//   - "" → ""
//   - plain:<原文> → 原文
//   - enc:v1:<payload> → AES-GCM 解密（认证失败返回 error）
//   - 其他（历史明文）→ 原样返回，实现无感迁移
func DecryptSecret(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if plain, ok := strings.CutPrefix(stored, PlainSecretPrefix); ok {
		return plain, nil
	}
	if !IsEncryptedSecret(stored) {
		return stored, nil
	}
	aead, err := secretAEADFor()
	if err != nil {
		return "", err
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(stored, EncryptedSecretPrefix))
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize() {
		return "", errors.New("encrypted secret payload is truncated")
	}
	nonce, ciphertext := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// DecryptSecretOrLegacy 宽容解密：解密失败时记录日志并返回原值，避免读路径
// 因密钥轮换/配置缺失直接不可用（调用方仍可依据 IsEncryptedSecret 拒绝上游调用）。
func DecryptSecretOrLegacy(stored string) string {
	plain, err := DecryptSecret(stored)
	if err != nil {
		SysError(LogText("secret decryption failed; returning stored value unchanged: %s", err.Error()))
		return stored
	}
	return plain
}

// MaskSecret 返回凭据脱敏提示（保留末 4 位），用于节点/渠道展示。
func MaskSecret(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) <= 4 {
		return "****"
	}
	return "****" + secret[len(secret)-4:]
}

func GenerateHMACWithKey(key []byte, data string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func GenerateHMAC(data string) string {
	h := hmac.New(sha256.New, []byte(CryptoSecret))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func Password2Hash(password string) (string, error) {
	passwordBytes := []byte(password)
	hashedPassword, err := bcrypt.GenerateFromPassword(passwordBytes, bcrypt.DefaultCost)
	return string(hashedPassword), err
}

func ValidatePasswordAndHash(password string, hash string) bool {
	if strings.HasPrefix(hash, "$argon2id$") {
		return validateArgon2AccountPassword(password, hash)
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
