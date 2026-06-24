package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractToken(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"empty", "", ""},
		{"bearer token", "Bearer eyJhbG...", "eyJhbG..."},
		{"bearer lowercase", "bearer eyJhbG...", "eyJhbG..."},
		{"bearer mixed case", "BeArEr eyJhbG...", "eyJhbG..."},
		{"no scheme", "eyJhbG...", ""},
		{"wrong scheme", "Basic dXNlcjpwYXNz", ""},
		{"bearer with extra spaces", "Bearer  eyJhbG...  ", "eyJhbG..."},
		{"bearer empty token", "Bearer ", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			got := ExtractToken(req)
			if got != tc.want {
				t.Errorf("ExtractToken() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractAPIKey(t *testing.T) {
	cases := []struct {
		name       string
		apiKeyHdr  string
		authHdr    string
		want       string
	}{
		{"x-api-key header", "mcp_abc123", "", "mcp_abc123"},
		{"bearer mcp_ prefix", "", "Bearer mcp_abc123", "mcp_abc123"},
		{"bearer without mcp_ prefix (OIDC token)", "", "Bearer eyJhbG...", ""},
		{"no headers", "", "", ""},
		{"x-api-key takes priority over bearer", "mcp_from_xkey", "Bearer mcp_from_bearer", "mcp_from_xkey"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tc.apiKeyHdr != "" {
				req.Header.Set("X-API-Key", tc.apiKeyHdr)
			}
			if tc.authHdr != "" {
				req.Header.Set("Authorization", tc.authHdr)
			}
			got := ExtractAPIKey(req)
			if got != tc.want {
				t.Errorf("ExtractAPIKey() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestJWTMiddleware_NoToken(t *testing.T) {
	authSvc := &AuthService{}
	req := httptest.NewRequest("GET", "/admin/servers", nil)
	w := httptest.NewRecorder()
	authSvc.JWTMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called without token")
	})).ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAPIKeyMiddleware_NoAuth(t *testing.T) {
	authSvc := &AuthService{}
	req := httptest.NewRequest("GET", "/api/servers", nil)
	w := httptest.NewRecorder()
	authSvc.APIKeyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called without auth")
	})).ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAPIKeyMiddleware_401ErrorBody(t *testing.T) {
	authSvc := &AuthService{}
	req := httptest.NewRequest("GET", "/api/servers", nil)
	w := httptest.NewRecorder()
	authSvc.APIKeyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called without auth")
	})).ServeHTTP(w, req)

	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
	body := w.Body.String()
	if !contains(body, "Missing or invalid API key") {
		t.Errorf("expected error message in body, got: %s", body)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
