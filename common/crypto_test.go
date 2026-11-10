package common

import (
	"strings"
	"testing"
)

func withCryptoSecret(t *testing.T, secret string, configured bool) {
	t.Helper()
	previousSecret := CryptoSecret
	previousConfigured := CryptoSecretConfigured
	CryptoSecret = secret
	CryptoSecretConfigured = configured
	t.Cleanup(func() {
		CryptoSecret = previousSecret
		CryptoSecretConfigured = previousConfigured
	})
}

func TestEncryptDecryptSecretRoundTrip(t *testing.T) {
	withCryptoSecret(t, "unit-test-secret", true)
	plaintext := `{"accessToken":"at-123","refreshToken":"rt-456"}`
	encrypted, err := EncryptSecret(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !IsEncryptedSecret(encrypted) {
		t.Fatalf("expected enc:v1 prefix, got %q", encrypted)
	}
	if strings.Contains(encrypted, plaintext) {
		t.Fatal("ciphertext must not contain plaintext")
	}
	decrypted, err := DecryptSecret(encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != plaintext {
		t.Fatalf("roundtrip mismatch: %q", decrypted)
	}
	// 同一明文两次加密必须产生不同密文（随机 nonce）。
	again, _ := EncryptSecret(plaintext)
	if again == encrypted {
		t.Fatal("expected random nonce to produce different ciphertext")
	}
}

func TestEncryptSecretWithoutCryptoSecretUsesPlainPrefix(t *testing.T) {
	withCryptoSecret(t, "fallback-session", false)
	encrypted, err := EncryptSecret("secret-value")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !IsPlainPrefixedSecret(encrypted) || encrypted != "plain:secret-value" {
		t.Fatalf("expected plain: prefix, got %q", encrypted)
	}
	decrypted, err := DecryptSecret(encrypted)
	if err != nil || decrypted != "secret-value" {
		t.Fatalf("decrypt plain: %q err=%v", decrypted, err)
	}
}

func TestDecryptSecretLegacyPlaintextAndTamper(t *testing.T) {
	withCryptoSecret(t, "unit-test-secret", true)
	legacy := `{"accessToken":"legacy"}`
	decrypted, err := DecryptSecret(legacy)
	if err != nil || decrypted != legacy {
		t.Fatalf("legacy plaintext must pass through: %q err=%v", decrypted, err)
	}
	encrypted, _ := EncryptSecret("value")
	tampered := encrypted[:len(encrypted)-2] + "AA"
	if _, err := DecryptSecret(tampered); err == nil {
		t.Fatal("expected authentication failure for tampered ciphertext")
	}
	// 换 secret 后无法解密（密钥轮换必须显式迁移）。
	withCryptoSecret(t, "another-secret", true)
	if _, err := DecryptSecret(encrypted); err == nil {
		t.Fatal("expected decryption failure after secret change")
	}
}

func TestMaskSecret(t *testing.T) {
	if got := MaskSecret("abcdefgh"); got != "****efgh" {
		t.Fatalf("unexpected mask: %q", got)
	}
	if got := MaskSecret("abc"); got != "****" {
		t.Fatalf("unexpected short mask: %q", got)
	}
	if got := MaskSecret(""); got != "" {
		t.Fatalf("unexpected empty mask: %q", got)
	}
}
