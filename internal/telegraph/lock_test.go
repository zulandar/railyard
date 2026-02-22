package telegraph

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openLockTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.DispatchSession{}, &models.TelegraphConversation{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

func TestAcquireLock_Success(t *testing.T) {
	db := openLockTestDB(t)

	session, err := AcquireLock(db, "telegraph", "alice", "thread-1", "C01", DefaultHeartbeatTimeout)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if session.ID == 0 {
		t.Fatal("expected session ID to be set")
	}
	if session.Source != "telegraph" {
		t.Errorf("Source = %q, want %q", session.Source, "telegraph")
	}
	if session.Status != "active" {
		t.Errorf("Status = %q, want %q", session.Status, "active")
	}
	if session.CarsCreated != "[]" {
		t.Errorf("CarsCreated = %q, want %q", session.CarsCreated, "[]")
	}
}

func TestAcquireLock_LocalSource(t *testing.T) {
	db := openLockTestDB(t)

	session, err := AcquireLock(db, "local", "bob", "", "", DefaultHeartbeatTimeout)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if session.Source != "local" {
		t.Errorf("Source = %q, want %q", session.Source, "local")
	}
}

func TestAcquireLock_Blocked(t *testing.T) {
	db := openLockTestDB(t)

	// First lock succeeds.
	_, err := AcquireLock(db, "telegraph", "alice", "thread-1", "C01", DefaultHeartbeatTimeout)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}

	// Second lock on same thread/channel fails.
	_, err = AcquireLock(db, "telegraph", "bob", "thread-1", "C01", DefaultHeartbeatTimeout)
	if err == nil {
		t.Fatal("expected error for second lock")
	}
	if !strings.Contains(err.Error(), "lock held by") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "lock held by")
	}
}

func TestAcquireLock_DifferentThreads(t *testing.T) {
	db := openLockTestDB(t)

	// Locks on different threads should not conflict.
	_, err := AcquireLock(db, "telegraph", "alice", "thread-1", "C01", DefaultHeartbeatTimeout)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}

	_, err = AcquireLock(db, "telegraph", "bob", "thread-2", "C01", DefaultHeartbeatTimeout)
	if err != nil {
		t.Fatalf("second AcquireLock (different thread): %v", err)
	}
}

func TestAcquireLock_ExpiresStale(t *testing.T) {
	db := openLockTestDB(t)

	// Create a session with a stale heartbeat.
	staleTime := time.Now().Add(-2 * time.Minute)
	staleSession := models.DispatchSession{
		Source:           "telegraph",
		UserName:         "alice",
		PlatformThreadID: "thread-1",
		ChannelID:        "C01",
		Status:           "active",
		CarsCreated:      "[]",
		LastHeartbeat:    staleTime,
	}
	db.Create(&staleSession)

	// Acquiring lock should expire the stale session and succeed.
	session, err := AcquireLock(db, "telegraph", "bob", "thread-1", "C01", DefaultHeartbeatTimeout)
	if err != nil {
		t.Fatalf("AcquireLock after stale: %v", err)
	}
	if session.UserName != "bob" {
		t.Errorf("UserName = %q, want %q", session.UserName, "bob")
	}

	// Verify stale session was expired.
	var old models.DispatchSession
	db.First(&old, staleSession.ID)
	if old.Status != "expired" {
		t.Errorf("stale session status = %q, want %q", old.Status, "expired")
	}
	if old.CompletedAt == nil {
		t.Error("stale session CompletedAt should be set")
	}
}

func TestAcquireLock_FreshHeartbeatNotExpired(t *testing.T) {
	db := openLockTestDB(t)

	// Create a session with a fresh heartbeat.
	freshSession := models.DispatchSession{
		Source:           "telegraph",
		UserName:         "alice",
		PlatformThreadID: "thread-1",
		ChannelID:        "C01",
		Status:           "active",
		CarsCreated:      "[]",
		LastHeartbeat:    time.Now(),
	}
	db.Create(&freshSession)

	// Acquiring lock should fail — fresh heartbeat should not be expired.
	_, err := AcquireLock(db, "telegraph", "bob", "thread-1", "C01", DefaultHeartbeatTimeout)
	if err == nil {
		t.Fatal("expected error — fresh session should not be expired")
	}
}

func TestAcquireLock_DefaultTimeout(t *testing.T) {
	db := openLockTestDB(t)

	// Passing 0 timeout should use DefaultHeartbeatTimeout.
	session, err := AcquireLock(db, "local", "alice", "t1", "c1", 0)
	if err != nil {
		t.Fatalf("AcquireLock with 0 timeout: %v", err)
	}
	if session.ID == 0 {
		t.Fatal("expected session to be created")
	}
}

func TestReleaseLock_Success(t *testing.T) {
	db := openLockTestDB(t)

	session, _ := AcquireLock(db, "telegraph", "alice", "thread-1", "C01", DefaultHeartbeatTimeout)

	if err := ReleaseLock(db, session.ID); err != nil {
		t.Fatalf("ReleaseLock: %v", err)
	}

	var released models.DispatchSession
	db.First(&released, session.ID)
	if released.Status != "completed" {
		t.Errorf("Status = %q, want %q", released.Status, "completed")
	}
	if released.CompletedAt == nil {
		t.Error("CompletedAt should be set")
	}
}

func TestReleaseLock_NotFound(t *testing.T) {
	db := openLockTestDB(t)

	err := ReleaseLock(db, 999)
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
	if !strings.Contains(err.Error(), "not found or not active") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found or not active")
	}
}

func TestReleaseLock_AlreadyCompleted(t *testing.T) {
	db := openLockTestDB(t)

	session, _ := AcquireLock(db, "telegraph", "alice", "thread-1", "C01", DefaultHeartbeatTimeout)
	ReleaseLock(db, session.ID)

	// Releasing again should fail.
	err := ReleaseLock(db, session.ID)
	if err == nil {
		t.Fatal("expected error releasing already-completed session")
	}
}

func TestReleaseLock_AllowsReacquire(t *testing.T) {
	db := openLockTestDB(t)

	session, _ := AcquireLock(db, "telegraph", "alice", "thread-1", "C01", DefaultHeartbeatTimeout)
	ReleaseLock(db, session.ID)

	// After release, a new lock should succeed on the same thread/channel.
	newSession, err := AcquireLock(db, "telegraph", "bob", "thread-1", "C01", DefaultHeartbeatTimeout)
	if err != nil {
		t.Fatalf("AcquireLock after release: %v", err)
	}
	if newSession.UserName != "bob" {
		t.Errorf("UserName = %q, want %q", newSession.UserName, "bob")
	}
}

func TestHeartbeat_Success(t *testing.T) {
	db := openLockTestDB(t)

	session, _ := AcquireLock(db, "telegraph", "alice", "thread-1", "C01", DefaultHeartbeatTimeout)
	originalHB := session.LastHeartbeat

	// Small sleep to ensure timestamp changes.
	time.Sleep(10 * time.Millisecond)

	if err := Heartbeat(db, session.ID); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	var updated models.DispatchSession
	db.First(&updated, session.ID)
	if !updated.LastHeartbeat.After(originalHB) {
		t.Error("LastHeartbeat should be updated after Heartbeat call")
	}
}

func TestHeartbeat_NotFound(t *testing.T) {
	db := openLockTestDB(t)

	err := Heartbeat(db, 999)
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
	if !strings.Contains(err.Error(), "not found or not active") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found or not active")
	}
}

func TestHeartbeat_CompletedSession(t *testing.T) {
	db := openLockTestDB(t)

	session, _ := AcquireLock(db, "telegraph", "alice", "thread-1", "C01", DefaultHeartbeatTimeout)
	ReleaseLock(db, session.ID)

	err := Heartbeat(db, session.ID)
	if err == nil {
		t.Fatal("expected error for completed session")
	}
}

func TestHeartbeat_PreventsExpiry(t *testing.T) {
	db := openLockTestDB(t)

	// Use a very short timeout.
	shortTimeout := 50 * time.Millisecond

	session, _ := AcquireLock(db, "telegraph", "alice", "thread-1", "C01", shortTimeout)

	// Heartbeat to keep it alive.
	time.Sleep(30 * time.Millisecond)
	Heartbeat(db, session.ID)

	// Wait past original timeout but not past refreshed heartbeat.
	time.Sleep(30 * time.Millisecond)

	// Lock should still be held (heartbeat refreshed it).
	_, err := AcquireLock(db, "telegraph", "bob", "thread-1", "C01", shortTimeout)
	if err == nil {
		t.Fatal("expected lock to still be held after heartbeat refresh")
	}
}

func TestConcurrent_AcquireLock_OneWinner(t *testing.T) {
	db := openLockTestDB(t)

	const goroutines = 10
	var winners atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			userName := fmt.Sprintf("user-%d", idx)
			_, err := AcquireLock(db, "telegraph", userName, "thread-race", "C-race", DefaultHeartbeatTimeout)
			if err == nil {
				winners.Add(1)
			}
		}(i)
	}

	wg.Wait()

	if got := winners.Load(); got != 1 {
		t.Errorf("concurrent lock winners = %d, want exactly 1", got)
	}
}

func TestConcurrent_HeartbeatDuringAcquire(t *testing.T) {
	db := openLockTestDB(t)

	// Acquire a lock, then heartbeat concurrently while another goroutine
	// tries to acquire the same lock.
	session, _ := AcquireLock(db, "telegraph", "alice", "thread-1", "C01", DefaultHeartbeatTimeout)

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: heartbeat repeatedly.
	go func() {
		defer wg.Done()
		for range 5 {
			Heartbeat(db, session.ID)
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// Goroutine 2: try to acquire (should fail).
	var acquireErr error
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		_, acquireErr = AcquireLock(db, "telegraph", "bob", "thread-1", "C01", DefaultHeartbeatTimeout)
	}()

	wg.Wait()
	if acquireErr == nil {
		t.Fatal("expected acquire to fail while lock is held and heartbeating")
	}
}
