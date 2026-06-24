package proxy

import (
	"testing"
)

func TestResolveEnvRefs_SimpleReference(t *testing.T) {
	envMap := map[string]string{
		"TOKEN": "${MY_SECRET}",
	}
	refVars := map[string]string{
		"MY_SECRET": "supersecret123",
	}
	result := resolveEnvRefs(envMap, refVars)
	if result["TOKEN"] != "supersecret123" {
		t.Errorf("expected supersecret123, got %s", result["TOKEN"])
	}
}

func TestResolveEnvRefs_DefaultValue(t *testing.T) {
	envMap := map[string]string{
		"TIMEOUT": "${MISSING_VAR:-30s}",
	}
	refVars := map[string]string{}
	result := resolveEnvRefs(envMap, refVars)
	if result["TIMEOUT"] != "30s" {
		t.Errorf("expected 30s default, got %s", result["TIMEOUT"])
	}
}

func TestResolveEnvRefs_UnknownKeyNoDefault(t *testing.T) {
	envMap := map[string]string{
		"TOKEN": "${UNKNOWN_KEY}",
	}
	refVars := map[string]string{}
	result := resolveEnvRefs(envMap, refVars)
	// Unknown keys without default should be left as-is
	if result["TOKEN"] != "${UNKNOWN_KEY}" {
		t.Errorf("expected ${UNKNOWN_KEY} unchanged, got %s", result["TOKEN"])
	}
}

func TestResolveEnvRefs_EmptyDefault(t *testing.T) {
	envMap := map[string]string{
		"VAR": "${MISSING:-}",
	}
	refVars := map[string]string{}
	result := resolveEnvRefs(envMap, refVars)
	if result["VAR"] != "" {
		t.Errorf("expected empty string for :- default, got %q", result["VAR"])
	}
}

func TestResolveEnvRefs_LiteralValue(t *testing.T) {
	envMap := map[string]string{
		"PATH":     "/usr/bin:/bin",
		"API_KEY":  "abc123",
		"DEBUG":    "true",
	}
	refVars := map[string]string{
		"OTHER": "unused",
	}
	result := resolveEnvRefs(envMap, refVars)
	for k, v := range envMap {
		if result[k] != v {
			t.Errorf("key %s: expected %s, got %s", k, v, result[k])
		}
	}
}

func TestResolveEnvRefs_MixedRefsAndLiterals(t *testing.T) {
	envMap := map[string]string{
		"TOKEN":    "${PAT}",
		"TIMEOUT":  "${TIMEOUT:-30s}",
		"PATH":     "/usr/bin",
		"COMBINED": "prefix-${PAT}-suffix",
	}
	refVars := map[string]string{
		"PAT": "token123",
	}
	result := resolveEnvRefs(envMap, refVars)

	if result["TOKEN"] != "token123" {
		t.Errorf("TOKEN: expected token123, got %s", result["TOKEN"])
	}
	if result["TIMEOUT"] != "30s" {
		t.Errorf("TIMEOUT: expected 30s, got %s", result["TIMEOUT"])
	}
	if result["PATH"] != "/usr/bin" {
		t.Errorf("PATH: expected /usr/bin, got %s", result["PATH"])
	}
	if result["COMBINED"] != "prefix-token123-suffix" {
		t.Errorf("COMBINED: expected prefix-token123-suffix, got %s", result["COMBINED"])
	}
}

func TestResolveEnvRefs_EmptyMap(t *testing.T) {
	result := resolveEnvRefs(map[string]string{}, map[string]string{"X": "y"})
	if len(result) != 0 {
		t.Errorf("expected empty result, got %v", result)
	}
}

func TestResolveEnvRefs_ReferenceOverriddenByDefault(t *testing.T) {
	// When the key exists in refVars, the default should NOT be used
	envMap := map[string]string{
		"VAR": "${KEY:-fallback}",
	}
	refVars := map[string]string{
		"KEY": "actual-value",
	}
	result := resolveEnvRefs(envMap, refVars)
	if result["VAR"] != "actual-value" {
		t.Errorf("expected actual-value (ref takes priority), got %s", result["VAR"])
	}
}

func TestResolveEnvRefs_DoesNotMutateInput(t *testing.T) {
	envMap := map[string]string{
		"TOKEN": "${MY_SECRET}",
	}
	original := envMap["TOKEN"]
	refVars := map[string]string{
		"MY_SECRET": "resolved-value",
	}
	_ = resolveEnvRefs(envMap, refVars)

	if envMap["TOKEN"] != original {
		t.Errorf("resolveEnvRefs mutated the input map: %s != %s", envMap["TOKEN"], original)
	}
}

func TestEnvVarRefPattern(t *testing.T) {
	cases := []struct {
		input    string
		matches  bool
		refKey   string
		default_ string
	}{
		{"${KEY}", true, "KEY", ""},
		{"${KEY:-default}", true, "KEY", "default"},
		{"${KEY:-}", true, "KEY", ""},
		{"${A_B_C}", true, "A_B_C", ""},
		{"${A1B2C3}", true, "A1B2C3", ""},
		{"${KEY:-with spaces}", true, "KEY", "with spaces"},
		{"${KEY:-https://example.com}", true, "KEY", "https://example.com"},
		{"${1KEY}", false, "", ""},        // must start with letter or _
		{"${KEY", false, "", ""},          // missing closing brace
		{"KEY", false, "", ""},            // no ${} wrapper
		{"${}", false, "", ""},            // empty key
		{"${KEY:-${NESTED}}", true, "KEY", "${NESTED"}, // nested not resolved, stops at first }
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			matches := envVarRefPattern.MatchString(tc.input)
			if matches != tc.matches {
				t.Errorf("MatchString(%q) = %v, want %v", tc.input, matches, tc.matches)
			}
			if matches {
				subs := envVarRefPattern.FindStringSubmatch(tc.input)
				if subs[1] != tc.refKey {
					t.Errorf("refKey: got %q, want %q", subs[1], tc.refKey)
				}
				if subs[2] != tc.default_ {
					t.Errorf("default: got %q, want %q", subs[2], tc.default_)
				}
			}
		})
	}
}
