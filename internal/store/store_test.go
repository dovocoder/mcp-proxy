package store

import (
	"testing"
	"time"

	"github.com/agentic/mcp-proxy/internal/crypto"
	"github.com/agentic/mcp-proxy/internal/models"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })
	return s
}

func newTestStoreWithEncryption(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	s.SetEncryptionKey(crypto.DeriveKey("test-master-key"))
	return s
}

func TestListEnvVarsDecrypted_NoEncryption(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	ev := &models.EnvVar{
		ID:        "ev-1",
		Project:   "default",
		Environment: "prod",
		Key:       "API_KEY",
		Value:     "plaintext-secret",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.CreateEnvVar(ev); err != nil {
		t.Fatalf("CreateEnvVar: %v", err)
	}

	result, err := s.ListEnvVarsDecrypted()
	if err != nil {
		t.Fatalf("ListEnvVarsDecrypted: %v", err)
	}
	if result["API_KEY"] != "plaintext-secret" {
		t.Errorf("expected plaintext-secret, got %s", result["API_KEY"])
	}
}

func TestListEnvVarsDecrypted_WithEncryption(t *testing.T) {
	s := newTestStoreWithEncryption(t)
	now := time.Now()

	// Store an encrypted env var directly
	encryptedVal, err := crypto.Encrypt(s.encKey, "my-decrypted-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	ev := &models.EnvVar{
		ID:        "ev-2",
		Project:   "default",
		Environment: "prod",
		Key:       "DB_PASSWORD",
		Value:     encryptedVal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.CreateEnvVar(ev); err != nil {
		t.Fatalf("CreateEnvVar: %v", err)
	}

	result, err := s.ListEnvVarsDecrypted()
	if err != nil {
		t.Fatalf("ListEnvVarsDecrypted: %v", err)
	}
	if result["DB_PASSWORD"] != "my-decrypted-secret" {
		t.Errorf("expected my-decrypted-secret, got %s", result["DB_PASSWORD"])
	}
}

func TestListEnvVarsDecrypted_MultipleVars(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	vars := []struct {
		key, value string
	}{
		{"VAR1", "val1"},
		{"VAR2", "val2"},
		{"VAR3", "val3"},
	}

	for i, v := range vars {
		ev := &models.EnvVar{
			ID:        string(rune('a' + i)),
			Project:   "default",
			Environment: "prod",
			Key:       v.key,
			Value:     v.value,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.CreateEnvVar(ev); err != nil {
			t.Fatalf("CreateEnvVar: %v", err)
		}
	}

	result, err := s.ListEnvVarsDecrypted()
	if err != nil {
		t.Fatalf("ListEnvVarsDecrypted: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 vars, got %d", len(result))
	}
	for _, v := range vars {
		if result[v.key] != v.value {
			t.Errorf("expected %s=%s, got %s", v.key, v.value, result[v.key])
		}
	}
}

func TestListEnvVarsDecrypted_EmptyDB(t *testing.T) {
	s := newTestStore(t)

	result, err := s.ListEnvVarsDecrypted()
	if err != nil {
		t.Fatalf("ListEnvVarsDecrypted on empty DB: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d items", len(result))
	}
}

func TestListEnvVarsDecrypted_SkipsBadDecryption(t *testing.T) {
	s := newTestStoreWithEncryption(t)
	now := time.Now()

	// Store a value that can't be decrypted (garbage, not valid encrypted data)
	ev := &models.EnvVar{
		ID:        "ev-bad",
		Project:   "default",
		Environment: "prod",
		Key:       "BAD_VAR",
		Value:     "this-is-not-valid-encrypted-data",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.CreateEnvVar(ev); err != nil {
		t.Fatalf("CreateEnvVar: %v", err)
	}

	// Also store a valid encrypted var
	goodVal, _ := crypto.Encrypt(s.encKey, "good-secret")
	ev2 := &models.EnvVar{
		ID:        "ev-good",
		Project:   "default",
		Environment: "prod",
		Key:       "GOOD_VAR",
		Value:     goodVal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.CreateEnvVar(ev2)

	result, err := s.ListEnvVarsDecrypted()
	if err != nil {
		t.Fatalf("ListEnvVarsDecrypted: %v", err)
	}
	// BAD_VAR should be skipped (decryption failed)
	if _, exists := result["BAD_VAR"]; exists {
		t.Error("BAD_VAR should have been skipped due to decryption failure")
	}
	// GOOD_VAR should still be present
	if result["GOOD_VAR"] != "good-secret" {
		t.Errorf("expected good-secret, got %s", result["GOOD_VAR"])
	}
}

func TestCreateAndGetServer(t *testing.T) {
	s := newTestStore(t)

	srv := &models.Server{
		ID:        "test-server-1",
		Name:      "Test Server",
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "@example/mcp"},
		Env:       map[string]string{"KEY": "value"},
		Enabled:   true,
	}
	if err := s.CreateServer(srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}

	got, err := s.GetServer("test-server-1")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if got.Name != "Test Server" {
		t.Errorf("expected Test Server, got %s", got.Name)
	}
	if got.Transport != "stdio" {
		t.Errorf("expected stdio, got %s", got.Transport)
	}
}
