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

func TestProjectEnvVarRefPattern(t *testing.T) {
	cases := []struct {
		input   string
		matches bool
		project string
		env     string
		varName string
	}{
		{"$[myapp:dev:TOKEN]", true, "myapp", "dev", "TOKEN"},
		{"$[my-app:prod:API_KEY]", true, "my-app", "prod", "API_KEY"},
		{"$[my.app:v2:SECRET_KEY]", true, "my.app", "v2", "SECRET_KEY"},
		{"$[a:b:GITHUB_TOKEN]", true, "a", "b", "GITHUB_TOKEN"},
		{"$[PROJ:ENV:VAR]", true, "PROJ", "ENV", "VAR"},
		{"prefix $[myapp:dev:TOKEN] suffix", true, "myapp", "dev", "TOKEN"},
		// Non-matches
		{"$[myapp:dev:1TOKEN]", false, "", "", ""},   // var must start with letter
		{"$[myapp:dev:]", false, "", "", ""},          // empty var name
		{"$[:dev:TOKEN]", false, "", "", ""},           // empty project
		{"$[myapp::TOKEN]", false, "", "", ""},          // empty env
		{"$[myapp:dev:TOKEN", false, "", "", ""},        // missing closing bracket
		{"$[myapp:dev]", false, "", "", ""},             // only 2 parts
		{"${myapp:dev:TOKEN}", false, "", "", ""},       // curly braces, not square
		// Edge: special chars in project/env names
		{"$[my_app:staging_eu:DB_PASS]", true, "my_app", "staging_eu", "DB_PASS"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			matches := projectEnvVarRefPattern.MatchString(tc.input)
			if matches != tc.matches {
				t.Errorf("MatchString(%q) = %v, want %v", tc.input, matches, tc.matches)
			}
			if matches {
				subs := projectEnvVarRefPattern.FindStringSubmatch(tc.input)
				if subs[1] != tc.project {
					t.Errorf("project: got %q, want %q", subs[1], tc.project)
				}
				if subs[2] != tc.env {
					t.Errorf("env: got %q, want %q", subs[2], tc.env)
				}
				if subs[3] != tc.varName {
					t.Errorf("var: got %q, want %q", subs[3], tc.varName)
				}
			}
		})
	}
}

func TestResolveEnvRefsWithGrouped_SimpleProjectRef(t *testing.T) {
	envMap := map[string]string{
		"TOKEN": "$[myapp:dev:GITHUB_TOKEN]",
	}
	groupedVars := map[string]string{
		"myapp:dev:GITHUB_TOKEN": "ghp_abcdef123",
	}
	result := resolveEnvRefsWithGrouped(envMap, nil, groupedVars)
	if result["TOKEN"] != "ghp_abcdef123" {
		t.Errorf("expected ghp_abcdef123, got %s", result["TOKEN"])
	}
}

func TestResolveEnvRefsWithGrouped_UnknownProjectRef(t *testing.T) {
	envMap := map[string]string{
		"TOKEN": "$[unknown:env:KEY]",
	}
	groupedVars := map[string]string{}
	result := resolveEnvRefsWithGrouped(envMap, nil, groupedVars)
	// Unknown $[...] refs should be left as-is
	if result["TOKEN"] != "$[unknown:env:KEY]" {
		t.Errorf("expected $[unknown:env:KEY] unchanged, got %s", result["TOKEN"])
	}
}

func TestResolveEnvRefsWithGrouped_MixedRefs(t *testing.T) {
	envMap := map[string]string{
		"TOKEN":    "$[myapp:dev:GITHUB_TOKEN]",
		"TIMEOUT":  "${TIMEOUT:-30s}",
		"PATH":     "/usr/bin",
		"COMBINED": "prefix-$[myapp:prod:SECRET]-suffix",
	}
	refVars := map[string]string{}
	groupedVars := map[string]string{
		"myapp:dev:GITHUB_TOKEN": "ghp_token123",
		"myapp:prod:SECRET":      "s3cr3t",
	}
	result := resolveEnvRefsWithGrouped(envMap, refVars, groupedVars)

	if result["TOKEN"] != "ghp_token123" {
		t.Errorf("TOKEN: expected ghp_token123, got %s", result["TOKEN"])
	}
	if result["TIMEOUT"] != "30s" {
		t.Errorf("TIMEOUT: expected 30s, got %s", result["TIMEOUT"])
	}
	if result["PATH"] != "/usr/bin" {
		t.Errorf("PATH: expected /usr/bin, got %s", result["PATH"])
	}
	if result["COMBINED"] != "prefix-s3cr3t-suffix" {
		t.Errorf("COMBINED: expected prefix-s3cr3t-suffix, got %s", result["COMBINED"])
	}
}

func TestResolveEnvRefsWithGrouped_ProjectAndFlatRef(t *testing.T) {
	// Both $[project:env:var] and ${KEY} in the same value
	envMap := map[string]string{
		"URL": "https://${HOST}/api?token=$[myapp:dev:TOKEN]",
	}
	refVars := map[string]string{
		"HOST": "api.example.com",
	}
	groupedVars := map[string]string{
		"myapp:dev:TOKEN": "abc123",
	}
	result := resolveEnvRefsWithGrouped(envMap, refVars, groupedVars)
	if result["URL"] != "https://api.example.com/api?token=abc123" {
		t.Errorf("URL: expected https://api.example.com/api?token=abc123, got %s", result["URL"])
	}
}

func TestResolveEnvRefsWithGrouped_NilGroupedVars(t *testing.T) {
	// When groupedVars is nil, $[...] refs should be left as-is
	envMap := map[string]string{
		"TOKEN": "$[myapp:dev:KEY]",
		"FLAT":  "${MY_VAR}",
	}
	refVars := map[string]string{
		"MY_VAR": "resolved",
	}
	result := resolveEnvRefsWithGrouped(envMap, refVars, nil)
	if result["TOKEN"] != "$[myapp:dev:KEY]" {
		t.Errorf("TOKEN: expected $[myapp:dev:KEY] unchanged, got %s", result["TOKEN"])
	}
	if result["FLAT"] != "resolved" {
		t.Errorf("FLAT: expected resolved, got %s", result["FLAT"])
	}
}
