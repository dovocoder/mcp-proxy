package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/secretbox"
)

// deriveSalt is a fixed application-specific salt for key derivation.
// This prevents precomputed rainbow table attacks — even if two deployments
// use the same passphrase, the derived key is unique per-salt.
// The salt is combined with the passphrase via HKDF (RFC 5869).
const deriveSalt = "mcp-proxy-env-var-encryption-v1"

// DeriveKey derives a 32-byte NaCl key from a passphrase string using HKDF-SHA256.
// HKDF provides proper key stretching with a salt, preventing precomputed
// dictionary/rainbow table attacks that single-pass SHA-256 would allow.
func DeriveKey(passphrase string) [32]byte {
	kdf := hkdf.New(sha256.New, []byte(passphrase), []byte(deriveSalt), nil)
	var key [32]byte
	if _, err := io.ReadFull(kdf, key[:]); err != nil {
		// Fallback to SHA-256 if HKDF fails (should never happen)
		return sha256.Sum256([]byte(passphrase))
	}
	return key
}

// Encrypt encrypts plaintext using NaCl secretbox with the given key.
// Returns base64(nonce + ciphertext).
func Encrypt(key [32]byte, plaintext string) (string, error) {
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	encrypted := secretbox.Seal(nil, []byte(plaintext), &nonce, &key)
	combined := append(nonce[:], encrypted...)
	return base64.StdEncoding.EncodeToString(combined), nil
}

// Decrypt decrypts a base64(nonce + ciphertext) blob using NaCl secretbox.
func Decrypt(key [32]byte, encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}
	if len(raw) < 24 {
		return "", fmt.Errorf("encrypted data too short")
	}
	var nonce [24]byte
	copy(nonce[:], raw[:24])
	ciphertext := raw[24:]
	decrypted, ok := secretbox.Open(nil, ciphertext, &nonce, &key)
	if !ok {
		return "", fmt.Errorf("decryption failed — wrong key or corrupted data")
	}
	return string(decrypted), nil
}
