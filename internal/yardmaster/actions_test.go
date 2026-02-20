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

func TestHandleRetryMerge_EpicAutoCloses(t *testing.T) {
	db := testDB(t)

	epicID := "epic-retry1"
	db.Create(&models.Car{ID: epicID, Type: "epic", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "child-r1", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})
	db.Create(&models.Car{ID: "child-r2", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})

	var buf bytes.Buffer
	msg := models.Message{Subject: "retry-merge", CarID: epicID, Body: "all children merged"}
	handleRetryMerge(db, msg, &buf)

	if !strings.Contains(buf.String(), "epic") {
		t.Errorf("output = %q, want to mention epic", buf.String())
	}

	var epic models.Car
	db.First(&epic, "id = ?", epicID)
	if epic.Status != "done" {
		t.Errorf("epic status = %q, want %q", epic.Status, "done")
	}
}

func TestHandleRetryMerge_EpicWithPendingChildren(t *testing.T) {
	db := testDB(t)

	epicID := "epic-retry2"
	db.Create(&models.Car{ID: epicID, Type: "epic", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "child-r3", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})
	db.Create(&models.Car{ID: "child-r4", Type: "task", Status: "open", Track: "backend", ParentID: &epicID})

	var buf bytes.Buffer
	msg := models.Message{Subject: "retry-merge", CarID: epicID, Body: "try closing"}
	handleRetryMerge(db, msg, &buf)

	var epic models.Car
	db.First(&epic, "id = ?", epicID)
	if epic.Status != "open" {
		t.Errorf("epic status = %q, want %q (child still open)", epic.Status, "open")
	}
}

func TestHandleRetryMerge_AcceptsMergeFailed(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-mf1", Type: "task", Status: "merge-failed", Track: "backend"})

	var buf bytes.Buffer
	msg := models.Message{Subject: "retry-merge", CarID: "car-mf1", Body: "fixed Docker"}
	handleRetryMerge(db, msg, &buf)

	var car models.Car
	db.First(&car, "id = ?", "car-mf1")
	if car.Status != "done" {
		t.Errorf("car status = %q, want %q", car.Status, "done")
	}
	if !strings.Contains(buf.String(), "setting car car-mf1 back to done") {
		t.Errorf("output = %q, want setting back to done", buf.String())
	}
}

func TestHandleRetryMerge_RejectsOpenStatus(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-mf2", Type: "task", Status: "open", Track: "backend"})

	var buf bytes.Buffer
	msg := models.Message{Subject: "retry-merge", CarID: "car-mf2", Body: "try again"}
	handleRetryMerge(db, msg, &buf)

	if !strings.Contains(buf.String(), "not blocked/merge-failed") {
		t.Errorf("output = %q, want rejection for open status", buf.String())
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

// --- handleCloseEpic ---

func TestHandleCloseEpic_NoCarID(t *testing.T) {
	var buf bytes.Buffer
	msg := models.Message{Subject: "close-epic", Body: "close it"}
	handleCloseEpic(nil, msg, &buf)

	if !strings.Contains(buf.String(), "no car-id provided") {
		t.Errorf("output = %q, want to contain %q", buf.String(), "no car-id provided")
	}
}

func TestHandleCloseEpic_ClosesEpic(t *testing.T) {
	db := testDB(t)

	epicID := "epic-close-act1"
	db.Create(&models.Car{ID: epicID, Type: "epic", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "child-ca1", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})
	db.Create(&models.Car{ID: "child-ca2", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})

	var buf bytes.Buffer
	msg := models.Message{Subject: "close-epic", CarID: epicID, Body: "all children merged"}
	handleCloseEpic(db, msg, &buf)

	if !strings.Contains(buf.String(), "auto-close") {
		t.Errorf("output = %q, want to mention auto-close", buf.String())
	}

	var epic models.Car
	db.First(&epic, "id = ?", epicID)
	if epic.Status != "done" {
		t.Errorf("epic status = %q, want %q", epic.Status, "done")
	}
}

func TestHandleCloseEpic_SkipsNonEpic(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "task-ce1", Type: "task", Status: "open", Track: "backend"})

	var buf bytes.Buffer
	msg := models.Message{Subject: "close-epic", CarID: "task-ce1", Body: "try closing"}
	handleCloseEpic(db, msg, &buf)

	if !strings.Contains(buf.String(), "not an epic") {
		t.Errorf("output = %q, want to mention not an epic", buf.String())
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
		{"close-epic", func(m models.Message, b *bytes.Buffer) { handleCloseEpic(nil, m, b) }},
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
