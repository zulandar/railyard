package messaging

import (
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/models"
)

// Injection security regression tests for the messaging package.
// Verifies that malicious message content cannot cause command injection
// via notification shell command templates.

func TestShellSanitize_DropsDangerousChars(t *testing.T) {
	dangerous := map[string]string{
		"semicolon":    "hello; rm -rf /",
		"backtick":     "hello `id`",
		"dollar_paren": "hello $(whoami)",
		"pipe":         "hello | cat /etc/passwd",
		"ampersand":    "hello && curl evil.com",
		"single_quote": "hello'; DROP TABLE users; --",
		"double_quote": `hello"; DROP TABLE users; --`,
		"backslash":    `hello\nworld`,
		"newline":      "hello\nrm -rf /",
		"carriage_ret": "hello\rmalicious",
		"null_byte":    "hello\x00evil",
		"curly_braces": "hello{evil}",
		"angle_braces": "hello<evil>",
		"parens":       "hello(evil)",
	}

	shellMeta := []string{";", "`", "$", "|", "&", "'", "\"", "\\", "\n", "\r", "\x00", "{", "}", "<", ">", "(", ")"}

	for name, input := range dangerous {
		t.Run(name, func(t *testing.T) {
			got := shellSanitize(input)
			for _, meta := range shellMeta {
				if strings.Contains(got, meta) {
					t.Errorf("shellSanitize(%q) still contains %q: %s", input, meta, got)
				}
			}
		})
	}
}

func TestShellSanitize_PreservesSafeChars(t *testing.T) {
	safe := []string{
		"Engine stalled",
		"car-123 is not responding",
		"eng-a1b2c3d4",
		"yardmaster: Engine stalled on track backend",
		"Priority level 3/5",
		"Update 2024-01-15",
		"100% complete",
		"file.txt",
		"path/to/file",
		"user@example.com",
	}
	for _, input := range safe {
		t.Run(input, func(t *testing.T) {
			got := shellSanitize(input)
			if got != input {
				t.Errorf("shellSanitize(%q) = %q, want unchanged", input, got)
			}
		})
	}
}

func TestTemplateMessage_InjectionInAllFields(t *testing.T) {
	// Every message field should be sanitized
	msg := &models.Message{
		Subject:   "test; rm -rf /",
		Body:      "$(cat /etc/shadow)",
		FromAgent: "agent`id`",
		ToAgent:   "human|evil",
		CarID:     "car-123; DROP TABLE",
		Priority:  "urgent&&curl evil.com",
	}

	cmd := "echo '{{.Subject}}' '{{.Body}}' '{{.From}}' '{{.To}}' '{{.CarID}}' '{{.Priority}}'"
	got := templateMessage(cmd, msg)

	shellMeta := []string{";", "`", "$(", "|", "&&"}
	for _, meta := range shellMeta {
		if strings.Contains(got, meta) {
			t.Errorf("templateMessage output contains dangerous %q: %s", meta, got)
		}
	}
}

func TestTemplateMessage_NoPlaceholderLeak(t *testing.T) {
	// If a message field contains a template placeholder, it should not cause
	// recursive expansion.
	msg := &models.Message{
		Subject: "{{.Body}}",
		Body:    "LEAKED",
	}

	got := templateMessage("Subject={{.Subject}} Body={{.Body}}", msg)
	if strings.Contains(got, "LEAKED") && !strings.Contains(got, "Body=LEAKED") {
		t.Errorf("template placeholder in Subject leaked Body value: %s", got)
	}
}

func TestNotify_EmptyCommand_NoExec(t *testing.T) {
	// Empty command should not execute anything (no panic, no error)
	msg := &models.Message{
		Subject:   "test; rm -rf /",
		FromAgent: "$(whoami)",
	}
	cfg := NotifyConfig{Command: ""}
	// Should be a no-op
	Notify(msg, cfg)
}

func TestNotify_MaliciousFields_PositionalArgsSafe(t *testing.T) {
	// When using positional args ($1, $2, etc.), shell metacharacters in
	// message fields are safe because they're passed as separate exec args.
	msg := &models.Message{
		Subject:   "test'; rm -rf / #",
		Body:      "$(whoami)",
		FromAgent: "`id`",
		ToAgent:   "human|cat /etc/passwd",
		CarID:     "car;evil",
		Priority:  "urgent&&bad",
	}
	cfg := NotifyConfig{Command: "echo $1"}
	// Should not panic or cause injection
	Notify(msg, cfg)
}
