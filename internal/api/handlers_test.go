package api

import "testing"

func TestValidateRedirectURI(t *testing.T) {
	valid := []string{
		"http://localhost:3000/callback",
		"https://localhost:3000/callback",
		"http://127.0.0.1:8080/callback",
		"https://127.0.0.1:443/callback",
		"http://[::1]:3000/callback",
		"https://[::1]:3000/callback",
		"com.raycast://callback",
		"com.example.app://oauth/callback",
		"myapp://auth",
	}

	for _, uri := range valid {
		t.Run("valid/"+uri, func(t *testing.T) {
			if err := validateRedirectURI(uri); err != nil {
				t.Errorf("expected %s to be valid, got error: %v", uri, err)
			}
		})
	}

	invalid := []struct {
		uri  string
		desc string
	}{
		{"", "empty"},
		{"not a url", "garbage"},
		{"file:///etc/passwd", "file scheme"},
		{"data:text/html,<script>alert(1)</script>", "data scheme"},
		{"javascript:alert(1)", "javascript scheme"},
		{"https://evil.com/steal", "external host"},
		{"https://192.168.1.1/callback", "private IP"},
		{"http://example.com/callback", "external host http"},
	}

	for _, tc := range invalid {
		t.Run("invalid/"+tc.desc, func(t *testing.T) {
			if err := validateRedirectURI(tc.uri); err == nil {
				t.Errorf("expected %s (%s) to be rejected", tc.uri, tc.desc)
			}
		})
	}
}
