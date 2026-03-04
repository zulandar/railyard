package messaging

import (
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDB creates an in-memory SQLite database with messaging tables.
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Message{},
		&models.BroadcastAck{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

// closedDB returns a GORM DB with the underlying sql.DB closed, forcing errors.
func closedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testDB(t)
	sqlDB, _ := db.DB()
	sqlDB.Close()
	return db
}

// --- Send DB tests ---

func TestSend_CreatesMessage(t *testing.T) {
	db := testDB(t)

	msg, err := Send(db, "engine-01", "yardmaster", "Help needed", "I'm stuck", SendOpts{
		CarID:    "car-abc12",
		Priority: "urgent",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if msg.ID == 0 {
		t.Error("expected auto-generated ID")
	}
	if msg.FromAgent != "engine-01" {
		t.Errorf("FromAgent = %q, want %q", msg.FromAgent, "engine-01")
	}
	if msg.ToAgent != "yardmaster" {
		t.Errorf("ToAgent = %q, want %q", msg.ToAgent, "yardmaster")
	}
	if msg.Subject != "Help needed" {
		t.Errorf("Subject = %q, want %q", msg.Subject, "Help needed")
	}
	if msg.Priority != "urgent" {
		t.Errorf("Priority = %q, want %q", msg.Priority, "urgent")
	}
	if msg.CarID != "car-abc12" {
		t.Errorf("CarID = %q, want %q", msg.CarID, "car-abc12")
	}
}

func TestSend_DefaultPriority(t *testing.T) {
	db := testDB(t)

	msg, err := Send(db, "engine-01", "yardmaster", "Status update", "All good", SendOpts{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if msg.Priority != "normal" {
		t.Errorf("Priority = %q, want %q (default)", msg.Priority, "normal")
	}
}

func TestSend_WithThreadID(t *testing.T) {
	db := testDB(t)

	tid := uint(42)
	msg, err := Send(db, "engine-01", "yardmaster", "Follow-up", "More info", SendOpts{
		ThreadID: &tid,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if msg.ThreadID == nil || *msg.ThreadID != 42 {
		t.Errorf("ThreadID = %v, want 42", msg.ThreadID)
	}
}

func TestSend_WithNotifyConfig(t *testing.T) {
	db := testDB(t)

	// Send to "human" with a no-op notify command — should trigger notification.
	msg, err := Send(db, "engine-01", "human", "Escalation", "Need help", SendOpts{
		Priority:     "urgent",
		NotifyConfig: &NotifyConfig{Command: "true"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if msg.ID == 0 {
		t.Error("expected message created")
	}
}

func TestSend_DBError(t *testing.T) {
	db := closedDB(t)

	_, err := Send(db, "engine-01", "yardmaster", "Test", "body", SendOpts{})
	if err == nil {
		t.Fatal("expected error from Send with closed DB")
	}
	if !strings.Contains(err.Error(), "messaging: send") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "messaging: send")
	}
}

// --- Inbox DB tests ---

func TestInbox_DirectMessages(t *testing.T) {
	db := testDB(t)

	Send(db, "engine-01", "yardmaster", "Msg 1", "body1", SendOpts{})
	Send(db, "engine-02", "yardmaster", "Msg 2", "body2", SendOpts{})
	Send(db, "engine-01", "engine-02", "Other agent", "body3", SendOpts{})

	msgs, err := Inbox(db, "yardmaster")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("Inbox: got %d, want 2", len(msgs))
	}
}

func TestInbox_ExcludesAcknowledged(t *testing.T) {
	db := testDB(t)

	msg, _ := Send(db, "engine-01", "yardmaster", "Msg 1", "body", SendOpts{})
	Send(db, "engine-02", "yardmaster", "Msg 2", "body", SendOpts{})

	Acknowledge(db, msg.ID)

	msgs, err := Inbox(db, "yardmaster")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("Inbox after ack: got %d, want 1", len(msgs))
	}
}

func TestInbox_IncludesUnackedBroadcasts(t *testing.T) {
	db := testDB(t)

	Send(db, "yardmaster", "broadcast", "Announcement", "body", SendOpts{})
	Send(db, "engine-01", "engine-02", "Direct", "body", SendOpts{})

	msgs, err := Inbox(db, "engine-02")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	// Should include: 1 direct message + 1 unacked broadcast = 2.
	if len(msgs) != 2 {
		t.Errorf("Inbox: got %d, want 2 (1 direct + 1 broadcast)", len(msgs))
	}
}

func TestInbox_ExcludesAckedBroadcasts(t *testing.T) {
	db := testDB(t)

	broadcast, _ := Send(db, "yardmaster", "broadcast", "Announcement", "body", SendOpts{})
	AcknowledgeBroadcast(db, broadcast.ID, "engine-01")

	msgs, err := Inbox(db, "engine-01")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("Inbox after broadcast ack: got %d, want 0", len(msgs))
	}
}

func TestInbox_PriorityOrdering(t *testing.T) {
	db := testDB(t)

	Send(db, "engine-01", "yardmaster", "Normal msg", "body", SendOpts{Priority: "normal"})
	Send(db, "engine-02", "yardmaster", "Urgent msg", "body", SendOpts{Priority: "urgent"})

	msgs, err := Inbox(db, "yardmaster")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("Inbox: got %d, want 2", len(msgs))
	}
	// "urgent" sorts after "normal" alphabetically with DESC — urgent comes first.
	if msgs[0].Priority != "urgent" {
		t.Errorf("first message priority = %q, want %q (urgent first)", msgs[0].Priority, "urgent")
	}
}

func TestInbox_DBError(t *testing.T) {
	db := closedDB(t)

	_, err := Inbox(db, "yardmaster")
	if err == nil {
		t.Fatal("expected error from Inbox with closed DB")
	}
	if !strings.Contains(err.Error(), "messaging: inbox") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "messaging: inbox")
	}
}

// --- Acknowledge DB tests ---

func TestAcknowledge_Success(t *testing.T) {
	db := testDB(t)

	msg, _ := Send(db, "engine-01", "yardmaster", "Test", "body", SendOpts{})

	if err := Acknowledge(db, msg.ID); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	// Verify the message is acknowledged.
	var check models.Message
	db.First(&check, msg.ID)
	if !check.Acknowledged {
		t.Error("message should be acknowledged after Acknowledge call")
	}
}

func TestAcknowledge_BroadcastRejected(t *testing.T) {
	db := testDB(t)

	broadcast, _ := Send(db, "yardmaster", "broadcast", "Announcement", "body", SendOpts{})

	err := Acknowledge(db, broadcast.ID)
	if err == nil {
		t.Fatal("expected error when acknowledging broadcast message")
	}
	if !strings.Contains(err.Error(), "not found or is broadcast") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found or is broadcast")
	}
}

func TestAcknowledge_NotFound(t *testing.T) {
	db := testDB(t)

	err := Acknowledge(db, 99999)
	if err == nil {
		t.Fatal("expected error for non-existent message")
	}
	if !strings.Contains(err.Error(), "not found or is broadcast") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found or is broadcast")
	}
}

func TestAcknowledge_DBError(t *testing.T) {
	db := closedDB(t)

	err := Acknowledge(db, 1)
	if err == nil {
		t.Fatal("expected error from Acknowledge with closed DB")
	}
	if !strings.Contains(err.Error(), "messaging: acknowledge") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "messaging: acknowledge")
	}
}

// --- AcknowledgeBroadcast DB tests ---

func TestAcknowledgeBroadcast_Success(t *testing.T) {
	db := testDB(t)

	broadcast, _ := Send(db, "yardmaster", "broadcast", "Announcement", "body", SendOpts{})

	if err := AcknowledgeBroadcast(db, broadcast.ID, "engine-01"); err != nil {
		t.Fatalf("AcknowledgeBroadcast: %v", err)
	}

	// Verify ack record exists.
	var count int64
	db.Model(&models.BroadcastAck{}).Where("message_id = ? AND agent_id = ?", broadcast.ID, "engine-01").Count(&count)
	if count != 1 {
		t.Errorf("broadcast_acks count = %d, want 1", count)
	}
}

func TestAcknowledgeBroadcast_DBError(t *testing.T) {
	db := closedDB(t)

	err := AcknowledgeBroadcast(db, 1, "engine-01")
	if err == nil {
		t.Fatal("expected error from AcknowledgeBroadcast with closed DB")
	}
	if !strings.Contains(err.Error(), "messaging: broadcast ack") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "messaging: broadcast ack")
	}
}

// --- GetThread DB tests ---

func TestGetThread_ReturnsOrdered(t *testing.T) {
	db := testDB(t)

	tid := uint(0)
	// Create first message (becomes thread root).
	msg1, _ := Send(db, "engine-01", "yardmaster", "Thread start", "body1", SendOpts{})
	tid = msg1.ID

	// Create follow-up messages in the thread.
	Send(db, "yardmaster", "engine-01", "Reply 1", "body2", SendOpts{ThreadID: &tid})
	Send(db, "engine-01", "yardmaster", "Reply 2", "body3", SendOpts{ThreadID: &tid})

	msgs, err := GetThread(db, tid)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("GetThread: got %d, want 2 (thread messages, not root)", len(msgs))
	}
	// Should be ordered by created_at ASC.
	if msgs[0].Subject != "Reply 1" {
		t.Errorf("first thread message = %q, want %q", msgs[0].Subject, "Reply 1")
	}
	if msgs[1].Subject != "Reply 2" {
		t.Errorf("second thread message = %q, want %q", msgs[1].Subject, "Reply 2")
	}
}

func TestGetThread_EmptyThread(t *testing.T) {
	db := testDB(t)

	msgs, err := GetThread(db, 99999)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("GetThread empty: got %d, want 0", len(msgs))
	}
}

func TestGetThread_DBError(t *testing.T) {
	db := closedDB(t)

	_, err := GetThread(db, 1)
	if err == nil {
		t.Fatal("expected error from GetThread with closed DB")
	}
	if !strings.Contains(err.Error(), "messaging: get thread") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "messaging: get thread")
	}
}

// --- Reply DB tests ---

func TestReply_CreatesReply(t *testing.T) {
	db := testDB(t)

	original, _ := Send(db, "engine-01", "yardmaster", "Need help", "body", SendOpts{
		Priority: "urgent",
	})

	reply, err := Reply(db, original.ID, "yardmaster", "Here's help")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}

	if reply.FromAgent != "yardmaster" {
		t.Errorf("FromAgent = %q, want %q", reply.FromAgent, "yardmaster")
	}
	if reply.ToAgent != "engine-01" {
		t.Errorf("ToAgent = %q, want %q (original sender)", reply.ToAgent, "engine-01")
	}
	if reply.Subject != "Re: Need help" {
		t.Errorf("Subject = %q, want %q", reply.Subject, "Re: Need help")
	}
	if reply.Priority != "urgent" {
		t.Errorf("Priority = %q, want %q (inherited)", reply.Priority, "urgent")
	}
}

func TestReply_InheritsThreadID(t *testing.T) {
	db := testDB(t)

	// Original has no thread — reply should set ThreadID = original.ID.
	original, _ := Send(db, "engine-01", "yardmaster", "Start", "body", SendOpts{})

	reply, err := Reply(db, original.ID, "yardmaster", "Response")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.ThreadID == nil {
		t.Fatal("ThreadID should be set")
	}
	if *reply.ThreadID != original.ID {
		t.Errorf("ThreadID = %d, want %d (original message ID)", *reply.ThreadID, original.ID)
	}
}

func TestReply_PreservesExistingThread(t *testing.T) {
	db := testDB(t)

	// Create a thread root.
	root, _ := Send(db, "engine-01", "yardmaster", "Root", "body", SendOpts{})
	tid := root.ID

	// Create a message in the thread.
	inThread, _ := Send(db, "yardmaster", "engine-01", "In thread", "body", SendOpts{ThreadID: &tid})

	// Reply to the in-thread message — should keep the existing ThreadID.
	reply, err := Reply(db, inThread.ID, "engine-01", "Back at you")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.ThreadID == nil || *reply.ThreadID != tid {
		t.Errorf("ThreadID = %v, want %d (preserved from parent)", reply.ThreadID, tid)
	}
}

func TestReply_ParentNotFound(t *testing.T) {
	db := testDB(t)

	_, err := Reply(db, 99999, "yardmaster", "Reply to nothing")
	if err == nil {
		t.Fatal("expected error for non-existent parent")
	}
	if !strings.Contains(err.Error(), "parent message") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "parent message")
	}
}

func TestReply_DBError(t *testing.T) {
	db := closedDB(t)

	_, err := Reply(db, 1, "yardmaster", "body")
	if err == nil {
		t.Fatal("expected error from Reply with closed DB")
	}
}
