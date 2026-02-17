package yardmaster

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/models"
)

// --- handleRestartEngine ---

func TestHandleRestartEngine_NoCarID(t *testing.T) {
	var buf bytes.Buffer
	msg := models.Message{Subject: "restart-engine", Body: "engine stuck"}
	handleRestartEngine(context.Background(), nil, nil, "railyard.yaml", msg, &buf)

	if !strings.Contains(buf.String(), "no car-id provided") {
		t.Errorf("output = %q, want to contain %q", buf.String(), "no car-id provided")
	}
}

func TestHandleRestartEngine_OutputPrefix(t *testing.T) {
	var buf bytes.Buffer
	msg := models.Message{Subject: "restart-engine"}
	handleRestartEngine(context.Background(), nil, nil, "railyard.yaml", msg, &buf)

	if !strings.HasPrefix(buf.String(), "Action restart-engine:") {
		t.Errorf("output = %q, want prefix %q", buf.String(), "Action restart-engine:")
	}
}

// --- handleRetryMerge ---

func TestHandleRetryMerge_NoCarID(t *testing.T) {
	var buf bytes.Buffer
	msg := models.Message{Subject: "retry-merge", Body: "fixed tests"}
	handleRetryMerge(nil, msg, &buf)

	if !strings.Contains(buf.String(), "no car-id provided") {
		t.Errorf("output = %q, want to contain %q", buf.String(), "no car-id provided")
	}
}

func TestHandleRetryMerge_OutputPrefix(t *testing.T) {
	var buf bytes.Buffer
	msg := models.Message{Subject: "retry-merge"}
	handleRetryMerge(nil, msg, &buf)

	if !strings.HasPrefix(buf.String(), "Action retry-merge:") {
		t.Errorf("output = %q, want prefix %q", buf.String(), "Action retry-merge:")
	}
}

// --- handleRequeueCar ---

func TestHandleRequeueCar_NoCarID(t *testing.T) {
	var buf bytes.Buffer
	msg := models.Message{Subject: "requeue-car", Body: "wrong approach"}
	handleRequeueCar(nil, msg, &buf)

	if !strings.Contains(buf.String(), "no car-id provided") {
		t.Errorf("output = %q, want to contain %q", buf.String(), "no car-id provided")
	}
}

func TestHandleRequeueCar_OutputPrefix(t *testing.T) {
	var buf bytes.Buffer
	msg := models.Message{Subject: "requeue-car"}
	handleRequeueCar(nil, msg, &buf)

	if !strings.HasPrefix(buf.String(), "Action requeue-car:") {
		t.Errorf("output = %q, want prefix %q", buf.String(), "Action requeue-car:")
	}
}

// --- handleNudgeEngine ---

func TestHandleNudgeEngine_NoCarID(t *testing.T) {
	var buf bytes.Buffer
	msg := models.Message{Subject: "nudge-engine", Body: "try approach X"}
	handleNudgeEngine(nil, msg, &buf)

	if !strings.Contains(buf.String(), "no car-id provided") {
		t.Errorf("output = %q, want to contain %q", buf.String(), "no car-id provided")
	}
}

func TestHandleNudgeEngine_OutputPrefix(t *testing.T) {
	var buf bytes.Buffer
	msg := models.Message{Subject: "nudge-engine"}
	handleNudgeEngine(nil, msg, &buf)

	if !strings.HasPrefix(buf.String(), "Action nudge-engine:") {
		t.Errorf("output = %q, want prefix %q", buf.String(), "Action nudge-engine:")
	}
}

// --- handleUnblockCar ---

func TestHandleUnblockCar_NoCarID(t *testing.T) {
	var buf bytes.Buffer
	msg := models.Message{Subject: "unblock-car", Body: "dep resolved"}
	handleUnblockCar(nil, msg, &buf)

	if !strings.Contains(buf.String(), "no car-id provided") {
		t.Errorf("output = %q, want to contain %q", buf.String(), "no car-id provided")
	}
}

func TestHandleUnblockCar_OutputPrefix(t *testing.T) {
	var buf bytes.Buffer
	msg := models.Message{Subject: "unblock-car"}
	handleUnblockCar(nil, msg, &buf)

	if !strings.HasPrefix(buf.String(), "Action unblock-car:") {
		t.Errorf("output = %q, want prefix %q", buf.String(), "Action unblock-car:")
	}
}

// --- All handlers skip on empty CarID ---

func TestAllHandlers_SkipOnEmptyCarID(t *testing.T) {
	handlers := []struct {
		name string
		run  func(models.Message, *bytes.Buffer)
	}{
		{"restart-engine", func(m models.Message, b *bytes.Buffer) {
			handleRestartEngine(context.Background(), nil, nil, "", m, b)
		}},
		{"retry-merge", func(m models.Message, b *bytes.Buffer) { handleRetryMerge(nil, m, b) }},
		{"requeue-car", func(m models.Message, b *bytes.Buffer) { handleRequeueCar(nil, m, b) }},
		{"nudge-engine", func(m models.Message, b *bytes.Buffer) { handleNudgeEngine(nil, m, b) }},
		{"unblock-car", func(m models.Message, b *bytes.Buffer) { handleUnblockCar(nil, m, b) }},
	}

	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			var buf bytes.Buffer
			msg := models.Message{Subject: h.name, Body: "test"}
			h.run(msg, &buf)

			output := buf.String()
			if !strings.Contains(output, "no car-id provided") {
				t.Errorf("%s: output = %q, want to contain %q", h.name, output, "no car-id provided")
			}
			if !strings.Contains(output, "skipping") {
				t.Errorf("%s: output = %q, want to contain %q", h.name, output, "skipping")
			}
		})
	}
}
