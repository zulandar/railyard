//go:build integration

package messaging

import (
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// --- Test helpers (same pattern as engine/integration_test.go) ---

type testDoltServer struct {
	Port int
	Dir  string
	cmd  *exec.Cmd
}

func startDoltServer(t *testing.T) *testDoltServer {
	t.Helper()
	dir := t.TempDir()

	for _, kv := range [][2]string{
		{"user.name", "Test Runner"},
		{"user.email", "test@railyard.dev"},
	} {
		cfg := exec.Command("dolt", "config", "--global", "--add", kv[0], kv[1])
		cfg.Dir = dir
		cfg.CombinedOutput()
	}

	init := exec.Command("dolt", "init")
	init.Dir = dir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("dolt init: %s\n%s", err, out)
	}

	port := freePort(t)
	cmd := exec.Command("dolt", "sql-server",
		"--port", fmt.Sprintf("%d", port),
		"--host", "127.0.0.1",
	)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatalf("dolt sql-server start: %v", err)
	}

	srv := &testDoltServer{Port: port, Dir: dir, cmd: cmd}
	t.Cleanup(func() {
		srv.cmd.Process.Kill()
		srv.cmd.Wait()
	})

	waitForServer(t, port)
	return srv
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func waitForServer(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("dolt sql-server not ready on port %d after 10s", port)
}

func setupTestDB(t *testing.T, dbName string) *gorm.DB {
	t.Helper()
	srv := startDoltServer(t)

	adminDB, err := db.ConnectAdmin("127.0.0.1", srv.Port)
	if err != nil {
		t.Fatalf("ConnectAdmin: %v", err)
	}
	if err := db.CreateDatabase(adminDB, dbName); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	gormDB, err := db.Connect("127.0.0.1", srv.Port, dbName)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := db.AutoMigrate(gormDB); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return gormDB
}

// --- Send tests ---

func TestIntegration_Send(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_send")

	msg, err := Send(gormDB, "eng-001", "yardmaster", "status", "all good", SendOpts{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if msg.ID == 0 {
		t.Error("expected non-zero message ID")
	}
	if msg.FromAgent != "eng-001" {
		t.Errorf("FromAgent = %q, want %q", msg.FromAgent, "eng-001")
	}
	if msg.ToAgent != "yardmaster" {
		t.Errorf("ToAgent = %q, want %q", msg.ToAgent, "yardmaster")
	}
	if msg.Subject != "status" {
		t.Errorf("Subject = %q, want %q", msg.Subject, "status")
	}
	if msg.Priority != "normal" {
		t.Errorf("Priority = %q, want %q", msg.Priority, "normal")
	}
	if msg.Acknowledged {
		t.Error("new message should not be acknowledged")
	}
}

func TestIntegration_Send_WithOpts(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_opts")

	tid := uint(99)
	msg, err := Send(gormDB, "eng-001", "eng-002", "help", "need review", SendOpts{
		CarID:   "car-123",
		ThreadID: &tid,
		Priority: "urgent",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if msg.CarID != "car-123" {
		t.Errorf("CarID = %q, want %q", msg.CarID, "car-123")
	}
	if msg.ThreadID == nil || *msg.ThreadID != 99 {
		t.Errorf("ThreadID = %v, want 99", msg.ThreadID)
	}
	if msg.Priority != "urgent" {
		t.Errorf("Priority = %q, want %q", msg.Priority, "urgent")
	}
}

// --- Inbox tests ---

func TestIntegration_Inbox(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_inbox")

	// Send two messages to eng-001 and one to eng-002.
	Send(gormDB, "yardmaster", "eng-001", "task1", "do this", SendOpts{})
	Send(gormDB, "yardmaster", "eng-001", "task2", "do that", SendOpts{})
	Send(gormDB, "yardmaster", "eng-002", "other", "not for eng-001", SendOpts{})

	msgs, err := Inbox(gormDB, "eng-001")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if msgs[0].Subject != "task1" {
		t.Errorf("msgs[0].Subject = %q, want %q", msgs[0].Subject, "task1")
	}
	if msgs[1].Subject != "task2" {
		t.Errorf("msgs[1].Subject = %q, want %q", msgs[1].Subject, "task2")
	}
}

func TestIntegration_Inbox_PriorityOrdering(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_prio")

	// Send normal first, then urgent.
	Send(gormDB, "yardmaster", "eng-001", "normal-task", "body", SendOpts{Priority: "normal"})
	Send(gormDB, "yardmaster", "eng-001", "urgent-task", "body", SendOpts{Priority: "urgent"})

	msgs, err := Inbox(gormDB, "eng-001")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	// "normal" < "urgent" alphabetically, so normal comes first in ASC order.
	if msgs[0].Subject != "normal-task" {
		t.Errorf("expected normal first (ASC sort), got %q", msgs[0].Subject)
	}
}

func TestIntegration_Inbox_Empty(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_empty")

	msgs, err := Inbox(gormDB, "eng-nobody")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty inbox, got %d messages", len(msgs))
	}
}

// --- Acknowledge tests ---

func TestIntegration_Acknowledge(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_ack")

	msg, _ := Send(gormDB, "yardmaster", "eng-001", "task", "do it", SendOpts{})

	if err := Acknowledge(gormDB, msg.ID); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	// Should not appear in inbox anymore.
	msgs, _ := Inbox(gormDB, "eng-001")
	if len(msgs) != 0 {
		t.Errorf("expected empty inbox after ack, got %d", len(msgs))
	}
}

func TestIntegration_Acknowledge_NotFound(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_acknotfound")

	err := Acknowledge(gormDB, 99999)
	if err == nil {
		t.Fatal("expected error for non-existent message")
	}
}

func TestIntegration_Acknowledge_BroadcastRejected(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_ackbc")

	// Send broadcast — Acknowledge should reject it.
	msg, _ := Send(gormDB, "yardmaster", "broadcast", "alert", "all agents", SendOpts{})

	err := Acknowledge(gormDB, msg.ID)
	if err == nil {
		t.Fatal("expected error for broadcast message")
	}
}

// --- Broadcast tests ---

func TestIntegration_Broadcast_AppearsInInbox(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_bcinbox")

	// Send broadcast.
	Send(gormDB, "yardmaster", "broadcast", "alert", "system update", SendOpts{})

	// Both eng-001 and eng-002 should see it.
	msgs1, _ := Inbox(gormDB, "eng-001")
	msgs2, _ := Inbox(gormDB, "eng-002")

	if len(msgs1) != 1 {
		t.Errorf("eng-001 inbox: want 1, got %d", len(msgs1))
	}
	if len(msgs2) != 1 {
		t.Errorf("eng-002 inbox: want 1, got %d", len(msgs2))
	}
}

func TestIntegration_AcknowledgeBroadcast(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_bcack")

	msg, _ := Send(gormDB, "yardmaster", "broadcast", "alert", "system update", SendOpts{})

	// eng-001 acks the broadcast.
	if err := AcknowledgeBroadcast(gormDB, msg.ID, "eng-001"); err != nil {
		t.Fatalf("AcknowledgeBroadcast: %v", err)
	}

	// eng-001 should no longer see it.
	msgs1, _ := Inbox(gormDB, "eng-001")
	if len(msgs1) != 0 {
		t.Errorf("eng-001 should not see acked broadcast, got %d", len(msgs1))
	}

	// eng-002 should still see it.
	msgs2, _ := Inbox(gormDB, "eng-002")
	if len(msgs2) != 1 {
		t.Errorf("eng-002 should still see broadcast, got %d", len(msgs2))
	}
}

// --- Threading tests ---

func TestIntegration_GetThread(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_thread")

	// Create a thread: send initial message, then reply.
	tid := uint(0) // will be set after first message
	msg1, _ := Send(gormDB, "eng-001", "yardmaster", "question", "how do I?", SendOpts{})
	tid = msg1.ID

	Send(gormDB, "yardmaster", "eng-001", "Re: question", "like this", SendOpts{ThreadID: &tid})
	Send(gormDB, "eng-001", "yardmaster", "Re: question", "thanks!", SendOpts{ThreadID: &tid})

	// Unrelated message (different thread).
	otherTid := uint(99999)
	Send(gormDB, "eng-002", "eng-003", "other", "unrelated", SendOpts{ThreadID: &otherTid})

	thread, err := GetThread(gormDB, tid)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	// Only the 2 messages with ThreadID=tid (not the first msg which has ThreadID=nil, not the other thread).
	if len(thread) != 2 {
		t.Fatalf("len(thread) = %d, want 2", len(thread))
	}
	if thread[0].Subject != "Re: question" {
		t.Errorf("thread[0].Subject = %q, want %q", thread[0].Subject, "Re: question")
	}
}

// --- Reply tests ---

func TestIntegration_Reply(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_reply")

	// Send initial message.
	msg1, _ := Send(gormDB, "eng-001", "yardmaster", "help", "I'm stuck", SendOpts{})

	// Reply to it.
	reply, err := Reply(gormDB, msg1.ID, "yardmaster", "try this approach")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}

	if reply.FromAgent != "yardmaster" {
		t.Errorf("FromAgent = %q, want %q", reply.FromAgent, "yardmaster")
	}
	if reply.ToAgent != "eng-001" {
		t.Errorf("ToAgent = %q, want %q", reply.ToAgent, "eng-001")
	}
	if reply.Subject != "Re: help" {
		t.Errorf("Subject = %q, want %q", reply.Subject, "Re: help")
	}
	if reply.ThreadID == nil || *reply.ThreadID != msg1.ID {
		t.Errorf("ThreadID = %v, want %d", reply.ThreadID, msg1.ID)
	}
}

func TestIntegration_Reply_InheritThread(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_replythread")

	// Send initial message with an explicit thread.
	existingThread := uint(42)
	msg1, _ := Send(gormDB, "eng-001", "yardmaster", "topic", "body", SendOpts{ThreadID: &existingThread})

	// Reply should inherit the existing thread.
	reply, err := Reply(gormDB, msg1.ID, "yardmaster", "response")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.ThreadID == nil || *reply.ThreadID != 42 {
		t.Errorf("ThreadID = %v, want 42", reply.ThreadID)
	}
}

// --- Mixed scenario ---

func TestIntegration_FullWorkflow(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_flow")

	// 1. Yardmaster sends direct + broadcast.
	direct, _ := Send(gormDB, "yardmaster", "eng-001", "abort", "stop car X", SendOpts{Priority: "urgent"})
	Send(gormDB, "yardmaster", "broadcast", "alert", "new priority", SendOpts{})

	// 2. eng-001 sees both.
	msgs, _ := Inbox(gormDB, "eng-001")
	if len(msgs) != 2 {
		t.Fatalf("eng-001 inbox: want 2, got %d", len(msgs))
	}

	// 3. Ack the direct message.
	Acknowledge(gormDB, direct.ID)
	msgs, _ = Inbox(gormDB, "eng-001")
	if len(msgs) != 1 {
		t.Fatalf("after ack: want 1, got %d", len(msgs))
	}

	// 4. Ack the broadcast for eng-001 only.
	bcMsg := msgs[0]
	AcknowledgeBroadcast(gormDB, bcMsg.ID, "eng-001")
	msgs, _ = Inbox(gormDB, "eng-001")
	if len(msgs) != 0 {
		t.Fatalf("after broadcast ack: want 0, got %d", len(msgs))
	}

	// 5. eng-002 should still see the broadcast.
	msgs2, _ := Inbox(gormDB, "eng-002")
	if len(msgs2) != 1 {
		t.Errorf("eng-002 should still see broadcast, got %d", len(msgs2))
	}

	// 6. Reply to the direct message.
	reply, _ := Reply(gormDB, direct.ID, "eng-001", "acked and stopping")
	if reply.ToAgent != "yardmaster" {
		t.Errorf("reply.ToAgent = %q, want %q", reply.ToAgent, "yardmaster")
	}

	// 7. Check yardmaster inbox sees the reply (plus the broadcast it sent).
	ymMsgs, _ := Inbox(gormDB, "yardmaster")
	// yardmaster sees: the reply + the broadcast (yardmaster hasn't acked it).
	if len(ymMsgs) != 2 {
		t.Fatalf("yardmaster inbox: want 2, got %d", len(ymMsgs))
	}
	// Find the reply in the inbox.
	foundReply := false
	for _, m := range ymMsgs {
		if m.Subject == "Re: abort" {
			foundReply = true
		}
	}
	if !foundReply {
		t.Error("yardmaster inbox should contain reply with subject 'Re: abort'")
	}
}

// --- Send error (closed DB) ---

func TestIntegration_Send_DBError(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_dberr")

	// Close the underlying connection to force an error.
	sqlDB, _ := gormDB.DB()
	sqlDB.Close()

	_, err := Send(gormDB, "eng-001", "yardmaster", "test", "body", SendOpts{})
	if err == nil {
		t.Fatal("expected error for closed DB")
	}
}

func TestIntegration_Inbox_DBError(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_inboxerr")

	sqlDB, _ := gormDB.DB()
	sqlDB.Close()

	_, err := Inbox(gormDB, "eng-001")
	if err == nil {
		t.Fatal("expected error for closed DB")
	}
}

func TestIntegration_GetThread_DBError(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_threaderr")

	sqlDB, _ := gormDB.DB()
	sqlDB.Close()

	_, err := GetThread(gormDB, 1)
	if err == nil {
		t.Fatal("expected error for closed DB")
	}
}

func TestIntegration_Acknowledge_DBError(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_ackerr")

	// First create a message, then close DB.
	msg, _ := Send(gormDB, "yardmaster", "eng-001", "test", "body", SendOpts{})

	sqlDB, _ := gormDB.DB()
	sqlDB.Close()

	err := Acknowledge(gormDB, msg.ID)
	if err == nil {
		t.Fatal("expected error for closed DB")
	}
}

func TestIntegration_AcknowledgeBroadcast_DBError(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_bcerr")

	sqlDB, _ := gormDB.DB()
	sqlDB.Close()

	err := AcknowledgeBroadcast(gormDB, 1, "eng-001")
	if err == nil {
		t.Fatal("expected error for closed DB")
	}
}

func TestIntegration_Reply_DBError(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_replyerr")

	// Create parent message first, then close DB.
	msg, _ := Send(gormDB, "eng-001", "yardmaster", "test", "body", SendOpts{})

	sqlDB, _ := gormDB.DB()
	sqlDB.Close()

	_, err := Reply(gormDB, msg.ID, "yardmaster", "reply body")
	if err == nil {
		t.Fatal("expected error for closed DB")
	}
}

// Verify BroadcastAck model is properly stored.
func TestIntegration_BroadcastAck_Model(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_msg_bcmodel")

	msg, _ := Send(gormDB, "yardmaster", "broadcast", "test", "body", SendOpts{})
	AcknowledgeBroadcast(gormDB, msg.ID, "eng-001")

	var ack models.BroadcastAck
	err := gormDB.Where("message_id = ? AND agent_id = ?", msg.ID, "eng-001").First(&ack).Error
	if err != nil {
		t.Fatalf("query BroadcastAck: %v", err)
	}
	if ack.MessageID != msg.ID {
		t.Errorf("MessageID = %d, want %d", ack.MessageID, msg.ID)
	}
	if ack.AgentID != "eng-001" {
		t.Errorf("AgentID = %q, want %q", ack.AgentID, "eng-001")
	}
}
