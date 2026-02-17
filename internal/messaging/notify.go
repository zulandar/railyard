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
func Notify(msg *models.Message, cfg NotifyConfig) {
	if cfg.Command != "" {
		cmdStr := templateMessage(cfg.Command, msg)
		cmd := exec.Command("sh", "-c", cmdStr)
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

// templateMessage replaces placeholders in the command template with message values.
func templateMessage(command string, msg *models.Message) string {
	r := strings.NewReplacer(
		"{{.Subject}}", msg.Subject,
		"{{.Body}}", msg.Body,
		"{{.From}}", msg.FromAgent,
		"{{.To}}", msg.ToAgent,
		"{{.CarID}}", msg.CarID,
		"{{.Priority}}", msg.Priority,
	)
	return r.Replace(command)
}
