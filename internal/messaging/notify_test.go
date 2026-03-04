package messaging

import (
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/models"
)

func TestShouldNotify_HumanTarget(t *testing.T) {
	msg := &models.Message{ToAgent: "human", Priority: "normal"}
	if !shouldNotify(msg) {
		t.Error("expected shouldNotify=true for human target")
	}
}

func TestShouldNotify_UrgentPriority(t *testing.T) {
	msg := &models.Message{ToAgent: "yardmaster", Priority: "urgent"}
	if !shouldNotify(msg) {
		t.Error("expected shouldNotify=true for urgent priority")
	}
}

func TestShouldNotify_NormalMessage(t *testing.T) {
	msg := &models.Message{ToAgent: "yardmaster", Priority: "normal"}
	if shouldNotify(msg) {
		t.Error("expected shouldNotify=false for normal non-human message")
	}
}

func TestTemplateMessage(t *testing.T) {
	msg := &models.Message{
		FromAgent: "yardmaster",
		ToAgent:   "human",
		Subject:   "Engine stalled",
		Body:      "Engine eng-abc is not responding",
		CarID:     "car-123",
		Priority:  "urgent",
	}

	cmd := "notify-send '{{.From}}: {{.Subject}}' '{{.Body}}' --urgency={{.Priority}}"
	got := templateMessage(cmd, msg)
	want := "notify-send 'yardmaster: Engine stalled' 'Engine eng-abc is not responding' --urgency=urgent"
	if got != want {
		t.Errorf("templateMessage =\n  %q\nwant\n  %q", got, want)
	}
}

func TestTemplateMessage_EmptyFields(t *testing.T) {
	msg := &models.Message{}
	got := templateMessage("{{.From}} {{.Subject}} {{.CarID}}", msg)
	want := "  "
	if got != want {
		t.Errorf("templateMessage = %q, want %q", got, want)
	}
}

func TestShellSanitize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		safe  bool // true if output should equal input (no dangerous chars)
	}{
		{"plain text", "Engine stalled", true},
		{"semicolon injection", "test; rm -rf /", false},
		{"backtick injection", "test `id`", false},
		{"dollar expansion", "test $(whoami)", false},
		{"pipe injection", "test | cat /etc/passwd", false},
		{"ampersand injection", "test && cat /etc/passwd", false},
		{"newline injection", "test\nrm -rf /", false},
		{"single quote breakout", "test'; rm -rf / #", false},
		{"double quote breakout", `test"; rm -rf / #`, false},
		{"safe with spaces", "Engine eng-abc is not responding", true},
		{"safe with hyphens and dots", "car-123.v2", true},
		{"safe with colons", "yardmaster: Engine stalled", true},
		{"safe with parens removed", "fix (auth)", false},
		{"empty string", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellSanitize(tt.input)
			if tt.safe && got != tt.input {
				t.Errorf("shellSanitize(%q) = %q, want unchanged", tt.input, got)
			}
			if !tt.safe && got == tt.input {
				t.Errorf("shellSanitize(%q) = %q, expected dangerous chars removed", tt.input, got)
			}
			// Must never contain injection characters
			for _, c := range []string{";", "`", "$(", "|", "&", "\n", "\r"} {
				if strings.Contains(got, c) {
					t.Errorf("shellSanitize(%q) still contains %q", tt.input, c)
				}
			}
		})
	}
}

func TestTemplateMessage_SanitizesInjection(t *testing.T) {
	msg := &models.Message{
		Subject:   "test'; rm -rf / #",
		Body:      "$(whoami)",
		FromAgent: "eng`id`",
	}
	got := templateMessage("echo '{{.Subject}}' '{{.Body}}' '{{.From}}'", msg)

	// The result must not contain shell metacharacters from the message values
	for _, dangerous := range []string{"; rm", "`id`", "$(whoami)"} {
		if strings.Contains(got, dangerous) {
			t.Errorf("templateMessage output contains dangerous %q: %s", dangerous, got)
		}
	}
}

func TestNotify_PassesPositionalArgs(t *testing.T) {
	// Verify that Notify passes message fields as positional args to sh -c
	// by using a command that echoes $1 (Subject)
	msg := &models.Message{
		Subject:   "hello world",
		FromAgent: "test",
		ToAgent:   "human",
	}
	cfg := NotifyConfig{Command: "echo $1"}
	// This tests the integration — the command should receive Subject as $1
	// We can't easily capture output here, so just verify it doesn't panic/error
	Notify(msg, cfg)
}
