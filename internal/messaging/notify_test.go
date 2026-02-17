package messaging

import (
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
