package pluginhost

import (
	"testing"
	"time"
)

func TestBumpActivityKnownPlugin(t *testing.T) {
	h := &Host{
		launched:     map[string]*launchedPlugin{"p": {name: "p"}},
		initFailures: map[string]initFailure{},
		clock:        func() time.Time { return time.Unix(1000, 0) },
	}
	h.bumpActivity("p")
	if got := h.launched["p"].lastActivity.Unix(); got != 1000 {
		t.Fatalf("lastActivity = %d, want 1000", got)
	}
}

func TestBumpActivityUnknownPluginIsNoOp(t *testing.T) {
	h := &Host{
		launched:     map[string]*launchedPlugin{},
		initFailures: map[string]initFailure{},
		clock:        func() time.Time { return time.Unix(1000, 0) },
	}
	// Must not panic.
	h.bumpActivity("never-existed")
}

func TestBumpActivityEmptyNameIsNoOp(t *testing.T) {
	h := &Host{
		launched:     map[string]*launchedPlugin{"p": {name: "p"}},
		initFailures: map[string]initFailure{},
		clock:        func() time.Time { return time.Unix(1000, 0) },
	}
	h.bumpActivity("")
	if !h.launched["p"].lastActivity.IsZero() {
		t.Fatalf("empty name should not bump anything; got %v", h.launched["p"].lastActivity)
	}
}
