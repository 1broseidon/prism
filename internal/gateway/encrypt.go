//go:build mcp_go_client_oauth

package gateway

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const kvEncKeyFile = "kv-encryption.key"

// kvEncryptionKey returns a 32-byte AES-256 key for encrypting secrets in
// the KV store. On first call it generates the key and persists it to
// $PRISM_KV_KEY_FILE (if set) or ~/.prism/kv-encryption.key with 0600
// permissions. Subsequent calls return the persisted key.
//
// If no usable key path can be determined (no home dir AND no override),
// the function returns an error rather than falling back to a deterministic
// host-derived key — that previous behaviour generated a key any attacker
// with the binary could reproduce offline, breaking encryption-at-rest for
// OAuth refresh tokens.
//
// Operators running prism in environments without a home directory (minimal
// containers, etc.) must set PRISM_KV_KEY_FILE to a writable path.
func kvEncryptionKey() ([]byte, error) {
	keyPath, err := keyFilePath()
	if err != nil {
		return nil, err
	}

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

	if mkErr := os.MkdirAll(filepath.Dir(keyPath), 0o700); mkErr != nil {
		return nil, fmt.Errorf("create kv encryption key dir: %w", mkErr)
	}
	if writeErr := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)), 0o600); writeErr != nil {
		return nil, fmt.Errorf("persist kv encryption key to %s: %w", keyPath, writeErr)
	}
	return key, nil
}

// keyFilePath resolves the encryption-key file location. Honours an explicit
// PRISM_KV_KEY_FILE override (useful for containers that mount a secret
// volume); otherwise uses ~/.prism/kv-encryption.key. Returns an error when
// neither is available — refusing to start beats silently using a derivable
// key.
func keyFilePath() (string, error) {
	if override := os.Getenv("PRISM_KV_KEY_FILE"); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate kv encryption key: no PRISM_KV_KEY_FILE override and no home dir: %w", err)
	}
	return filepath.Join(home, ".prism", kvEncKeyFile), nil
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
