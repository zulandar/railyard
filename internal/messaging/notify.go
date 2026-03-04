package messaging

import (
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/zulandar/railyard/internal/models"
)

// NotifyConfig controls how notifications are delivered for human-targeted messages.
type NotifyConfig struct {
	Command string // shell command template, e.g. "notify-send 'Railyard' '{{.Subject}}'"
}

// Notify sends a desktop notification for a message. Best-effort: errors are logged, not returned.
//
// Message fields are available to the command in two ways:
//   - Positional args (RECOMMENDED, injection-safe): $1=Subject, $2=Body, $3=From, $4=To, $5=CarID, $6=Priority
//   - Template placeholders (legacy, sanitized): {{.Subject}}, {{.Body}}, {{.From}}, {{.To}}, {{.CarID}}, {{.Priority}}
//
// Example (safe):  "notify-send 'Railyard' \"$1\""
// Example (legacy): "notify-send 'Railyard' '{{.Subject}}'"
func Notify(msg *models.Message, cfg NotifyConfig) {
	if cfg.Command != "" {
		cmdStr := templateMessage(cfg.Command, msg)
		cmd := exec.Command("sh", "-c", cmdStr, "_",
			msg.Subject, msg.Body, msg.FromAgent, msg.ToAgent, msg.CarID, msg.Priority)
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Printf("notify: command failed: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}

	// If inside tmux, also display a tmux message.
	if os.Getenv("TMUX") != "" {
		tmuxMsg := msg.FromAgent + ": " + msg.Subject
		cmd := exec.Command("tmux", "display-message", tmuxMsg)
		if err := cmd.Run(); err != nil {
			log.Printf("notify: tmux display-message failed: %v", err)
		}
	}
}

// shouldNotify returns true if the message warrants a push notification.
func shouldNotify(msg *models.Message) bool {
	return msg.ToAgent == "human" || msg.Priority == "urgent"
}

// shellSanitize removes shell metacharacters from a string to prevent command injection.
// Used when substituting message values into shell command templates.
func shellSanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\'', '"', '`', '$', ';', '|', '&', '(', ')', '{', '}',
			'<', '>', '\\', '\n', '\r', '\x00':
			// drop dangerous characters
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// templateMessage replaces placeholders in the command template with sanitized message values.
// All values are passed through shellSanitize to prevent command injection.
func templateMessage(command string, msg *models.Message) string {
	r := strings.NewReplacer(
		"{{.Subject}}", shellSanitize(msg.Subject),
		"{{.Body}}", shellSanitize(msg.Body),
		"{{.From}}", shellSanitize(msg.FromAgent),
		"{{.To}}", shellSanitize(msg.ToAgent),
		"{{.CarID}}", shellSanitize(msg.CarID),
		"{{.Priority}}", shellSanitize(msg.Priority),
	)
	return r.Replace(command)
}
