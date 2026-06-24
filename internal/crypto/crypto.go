package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/nacl/secretbox"
)

// DeriveKey derives a 32-byte NaCl key from a passphrase string using SHA-256.
func DeriveKey(passphrase string) [32]byte {
	return sha256.Sum256([]byte(passphrase))
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
