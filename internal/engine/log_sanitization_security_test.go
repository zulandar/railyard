package engine

import (
	"strings"
	"testing"
)

// TestLogSanitizationMultipleSecrets verifies that a single log line containing
// multiple secret types has all of them redacted.
func TestLogSanitizationMultipleSecrets(t *testing.T) {
	apiKey := "sk-proj_abc123DEF456ghi789jkl012mno"
	ghPAT := "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ012345678a"

	input := "Calling API with key=" + apiKey + " and github token=" + ghPAT
	result := redactSecrets(input)

	if strings.Contains(result, apiKey) {
		t.Errorf("API key was not redacted: %s", result)
	}
	if strings.Contains(result, ghPAT) {
		t.Errorf("GitHub PAT was not redacted: %s", result)
	}
	// Both should become [REDACTED]
	count := strings.Count(result, "[REDACTED]")
	if count < 2 {
		t.Errorf("expected at least 2 redactions, got %d in: %s", count, result)
	}
}

// TestLogSanitizationPartialMatchSkPrefix ensures that "sk-" alone or with
// fewer than 20 trailing characters is NOT redacted (too short to be a real key).
func TestLogSanitizationPartialMatchSkPrefix(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // expected unchanged
	}{
		{"bare prefix", "sk-", "sk-"},
		{"short suffix", "sk-short", "sk-short"},
		{"19 chars", "sk-1234567890123456789", "sk-1234567890123456789"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := redactSecrets(tc.input)
			if result != tc.want {
				t.Errorf("partial match was incorrectly redacted: input=%q got=%q want=%q", tc.input, result, tc.want)
			}
		})
	}
}

// TestLogSanitizationDSNPattern verifies that DSN connection strings with
// embedded credentials are redacted.
func TestLogSanitizationDSNPattern(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"postgresql DSN",
			"postgresql://admin:supersecretpassword@db.example.com:5432/mydb",
		},
		{
			"mysql DSN",
			"mysql://root:p4ssw0rd!!@localhost:3306/app",
		},
		{
			"redis DSN",
			"redis://default:longRedisPassword@cache.internal:6379/0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := redactSecrets(tc.input)
			if result == tc.input {
				t.Errorf("DSN credentials were not redacted: %s", result)
			}
			if !strings.Contains(result, "[REDACTED]") {
				t.Errorf("expected [REDACTED] in output, got: %s", result)
			}
		})
	}
}

// TestLogSanitizationSlackTokens verifies Slack bot and user tokens are redacted.
func TestLogSanitizationSlackTokens(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{"slack bot token", "xoxb-FAKE-TEST-TOKEN-nottreal"},
		{"slack user token", "xoxp-FAKE-TEST-TOKEN-nottreal"},
		{"slack app token", "xapp-FAKE-TEST-TOKEN-nottreal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := "Using token: " + tc.token
			result := redactSecrets(input)
			if strings.Contains(result, tc.token) {
				t.Errorf("%s was not redacted: %s", tc.name, result)
			}
		})
	}
}

// TestLogSanitizationAWSAccessKeys verifies AWS access key IDs are redacted.
func TestLogSanitizationAWSAccessKeys(t *testing.T) {
	awsKey := "AKIAIOSFODNN7EXAMPLE"
	input := "aws_access_key_id = " + awsKey
	result := redactSecrets(input)

	if strings.Contains(result, awsKey) {
		t.Errorf("AWS access key was not redacted: %s", result)
	}
	if !strings.Contains(result, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in output, got: %s", result)
	}
}

// TestLogSanitizationNoFalsePositives ensures normal log content passes
// through unchanged.
func TestLogSanitizationNoFalsePositives(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple log", "2026-01-15T10:30:00Z INFO server started on port 8080"},
		{"go stack trace", "goroutine 1 [running]:\nmain.main()\n\t/app/main.go:42"},
		{"file path", "reading config from /etc/railyard/config.yaml"},
		{"json log", `{"level":"info","msg":"request completed","status":200,"duration":"12ms"}`},
		{"git hash", "commit abc123def456789012345678901234567890abcd"},
		{"url without creds", "https://api.example.com/v1/trains?page=2&limit=50"},
		{"sk prefix in word", "the task-driven approach works well"},
		{"short passwords in dsn", "user:pw@host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := redactSecrets(tc.input)
			if result != tc.input {
				t.Errorf("false positive: input=%q got=%q", tc.input, result)
			}
		})
	}
}

// TestLogSanitizationEdgeCases covers empty strings and very long content.
func TestLogSanitizationEdgeCases(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		result := redactSecrets("")
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("very long content without secrets", func(t *testing.T) {
		// 100KB of normal log content
		line := "2026-01-15T10:30:00Z INFO processing request id=12345\n"
		longContent := strings.Repeat(line, 2000)
		result := redactSecrets(longContent)
		if result != longContent {
			t.Error("long content without secrets was modified")
		}
	})

	t.Run("very long content with embedded secret", func(t *testing.T) {
		line := "2026-01-15T10:30:00Z INFO processing request\n"
		prefix := strings.Repeat(line, 1000)
		secret := "sk-abcdefghijklmnopqrstuvwxyz1234567890"
		suffix := strings.Repeat(line, 1000)
		input := prefix + secret + suffix
		result := redactSecrets(input)

		if strings.Contains(result, secret) {
			t.Error("secret embedded in long content was not redacted")
		}
		if !strings.Contains(result, "[REDACTED]") {
			t.Error("expected [REDACTED] in long content output")
		}
	})
}

// TestRedactBearerTokens verifies Bearer token redaction.
func TestRedactBearerTokens(t *testing.T) {
	token := "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkw"
	input := "Authorization: " + token
	result := redactSecrets(input)

	if strings.Contains(result, "eyJhbGci") {
		t.Errorf("Bearer token was not redacted: %s", result)
	}
}

// TestRedactGitHubFineGrainedPAT verifies fine-grained GitHub PATs are redacted.
func TestRedactGitHubFineGrainedPAT(t *testing.T) {
	// Fine-grained PATs: github_pat_ followed by 60+ alphanumeric/underscore chars
	pat := "github_pat_" + strings.Repeat("a1B2c3D4e5F6g7H8i9J0", 4) // 80 chars after prefix
	input := "token=" + pat
	result := redactSecrets(input)

	if strings.Contains(result, pat) {
		t.Errorf("fine-grained GitHub PAT was not redacted: %s", result)
	}
}
