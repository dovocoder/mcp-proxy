package crypto

import (
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := DeriveKey("test-passphrase")
	cases := []string{
		"hello world",
		"PERSONAL_ACCESS_TOKEN=ghp_1234567890",
		"",
		"unicode: 日本語 🎉",
		"very long string " + string(make([]byte, 1000)),
	}

	for _, plain := range cases {
		encoded, err := Encrypt(key, plain)
		if err != nil {
			t.Fatalf("Encrypt failed: %v", err)
		}
		decoded, err := Decrypt(key, encoded)
		if err != nil {
			t.Fatalf("Decrypt failed: %v", err)
		}
		if decoded != plain {
			t.Errorf("round-trip mismatch: got %q, want %q", decoded, plain)
		}
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := DeriveKey("passphrase-1")
	key2 := DeriveKey("passphrase-2")

	encoded, err := Encrypt(key1, "secret")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	_, err = Decrypt(key2, encoded)
	if err == nil {
		t.Fatal("Decrypt with wrong key should fail")
	}
}

func TestDecryptCorruptedData(t *testing.T) {
	key := DeriveKey("test-passphrase")

	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"too short", "aGk="}, // 2 bytes, less than nonce size
		{"invalid base64", "not!!!base64!!!"},
		{"valid base64 but garbage", "dGhpcyBpcyBnYXJiYWdlIGRhdGE="},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decrypt(key, tc.input)
			if err == nil {
				t.Fatalf("Decrypt should fail for %s", tc.name)
			}
		})
	}
}

func TestDeriveKeyDeterministic(t *testing.T) {
	k1 := DeriveKey("same-passphrase")
	k2 := DeriveKey("same-passphrase")
	if k1 != k2 {
		t.Fatal("DeriveKey should be deterministic for the same passphrase")
	}

	k3 := DeriveKey("different-passphrase")
	if k1 == k3 {
		t.Fatal("DeriveKey should produce different keys for different passphrases")
	}
}

func TestEncryptProducesDifferentCiphertext(t *testing.T) {
	key := DeriveKey("test-passphrase")
	plaintext := "same input"

	c1, _ := Encrypt(key, plaintext)
	c2, _ := Encrypt(key, plaintext)
	if c1 == c2 {
		t.Fatal("Encrypt should produce different ciphertexts due to random nonce")
	}

	// Both should decrypt to the same value
	d1, _ := Decrypt(key, c1)
	d2, _ := Decrypt(key, c2)
	if d1 != d2 || d1 != plaintext {
		t.Fatal("Both ciphertexts should decrypt to the same plaintext")
	}
}
