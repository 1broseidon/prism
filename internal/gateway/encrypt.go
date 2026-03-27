//go:build mcp_go_client_oauth

package gateway

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const kvEncKeyFile = "kv-encryption.key"

// kvEncryptionKey returns a 32-byte AES-256 key for encrypting secrets in the
// KV store. On first call it generates the key and persists it to
// ~/.prism/kv-encryption.key (0600). Subsequent calls return the persisted key.
// If the key file cannot be created (e.g. no home dir), a deterministic
// fallback derived from the hostname is used so the process can still start.
func kvEncryptionKey() ([]byte, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return fallbackKey(), nil
	}

	dir := filepath.Join(home, ".prism")
	keyPath := filepath.Join(dir, kvEncKeyFile)

	// Try to read existing key.
	if data, readErr := os.ReadFile(keyPath); readErr == nil && len(data) == 64 {
		key, decErr := hex.DecodeString(string(data))
		if decErr == nil && len(key) == 32 {
			return key, nil
		}
	}

	// Generate a new key.
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate encryption key: %w", err)
	}

	_ = os.MkdirAll(dir, 0o700)
	if writeErr := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)), 0o600); writeErr != nil {
		// Key generated but not persisted — tokens won't survive restart.
		// Still usable for this session.
		return key, nil
	}

	return key, nil
}

// fallbackKey derives a deterministic key from hostname. Not ideal but allows
// the process to start when the home directory is unavailable.
func fallbackKey() []byte {
	host, _ := os.Hostname()
	h := sha256.Sum256([]byte("prism-kv-fallback:" + host))
	return h[:]
}

// encryptAESGCM encrypts plaintext with AES-256-GCM using the provided key.
// Returns nonce || ciphertext.
func encryptAESGCM(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decryptAESGCM decrypts data produced by encryptAESGCM.
func decryptAESGCM(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce := ciphertext[:gcm.NonceSize()]
	return gcm.Open(nil, nonce, ciphertext[gcm.NonceSize():], nil)
}
