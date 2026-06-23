package engine

import (
	"testing"

	"github.com/zulandar/railyard/internal/messaging"
)

// TestRegister_IgnoresStaleBroadcastDrain reproduces railyard-d3n: a drain
// broadcast left in the messages table by a prior `ry stop` must NOT cause a
// freshly registered engine to shut down on its first poll cycle. Register
// acks the pre-existing broadcast backlog so a new engine only obeys
// broadcasts sent after it came online.
func TestRegister_IgnoresStaleBroadcastDrain(t *testing.T) {
	gormDB := heartbeatTestDB(t)

	// A prior `ry stop` left this broadcast drain behind.
	if _, err := messaging.Send(gormDB, "orchestrator", "broadcast", "drain",
		"Railyard shutting down.", messaging.SendOpts{}); err != nil {
		t.Fatalf("seed drain broadcast: %v", err)
	}

	// New engine registers afterwards (fresh ID).
	eng, err := Register(gormDB, RegisterOpts{Track: "default", Provider: "claude"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	instructions, err := ProcessInbox(gormDB, eng.ID)
	if err != nil {
		t.Fatalf("process inbox: %v", err)
	}

	if ShouldDrain(instructions) {
		t.Fatalf("new engine drained on a stale pre-registration broadcast; want no drain")
	}
}

// TestRegister_HonorsBroadcastDrainAfterRegistration guards against
// over-acking: a drain broadcast sent AFTER the engine registers must still
// drain it.
func TestRegister_HonorsBroadcastDrainAfterRegistration(t *testing.T) {
	gormDB := heartbeatTestDB(t)

	eng, err := Register(gormDB, RegisterOpts{Track: "default", Provider: "claude"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Live drain arrives after the engine is online.
	if _, err := messaging.Send(gormDB, "orchestrator", "broadcast", "drain",
		"Railyard shutting down.", messaging.SendOpts{}); err != nil {
		t.Fatalf("send drain broadcast: %v", err)
	}

	instructions, err := ProcessInbox(gormDB, eng.ID)
	if err != nil {
		t.Fatalf("process inbox: %v", err)
	}

	if !ShouldDrain(instructions) {
		t.Fatalf("engine ignored a live post-registration drain; want drain")
	}
}
