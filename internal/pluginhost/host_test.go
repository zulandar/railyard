package pluginhost

import (
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
)

// TestYardInfoFromConfig verifies YardInfo is populated from the supplied
// config and is cached (returned verbatim on every call). Predates the
// subprocess plugin model and remains valid: it only exercises NewHost +
// the deterministic YardInfo accessor.
//
// Also exercises the legacy YardID-from-Project fallback: when cfg.YardID
// is empty, plugin.YardInfo.YardID must equal cfg.Project so existing
// configs see no behavior change.
func TestYardInfoFromConfig(t *testing.T) {
	cfg := &config.Config{
		Owner:   "zulandar",
		Project: "railyard",
		Repo:    "https://github.com/zulandar/railyard",
	}
	host := NewHost(Dependencies{
		Cfg:             cfg,
		RailyardVersion: "v9.9.9",
		BuildCommit:     "deadbeef",
		BuildTime:       time.Unix(1700000000, 0).UTC(),
	})

	info := host.YardInfo()
	if info.Owner != "zulandar" {
		t.Errorf("Owner = %q, want %q", info.Owner, "zulandar")
	}
	if info.Project != "railyard" {
		t.Errorf("Project = %q, want %q", info.Project, "railyard")
	}
	if info.RepoURL != "https://github.com/zulandar/railyard" {
		t.Errorf("RepoURL = %q", info.RepoURL)
	}
	if info.RailyardVersion != "v9.9.9" {
		t.Errorf("RailyardVersion = %q, want v9.9.9", info.RailyardVersion)
	}
	if info.BuildCommit != "deadbeef" {
		t.Errorf("BuildCommit = %q", info.BuildCommit)
	}
	// Fallback path: no cfg.YardID set, so YardInfo.YardID must equal Project.
	if info.YardID != "railyard" {
		t.Errorf("YardID = %q, want %q (fallback to Project)", info.YardID, "railyard")
	}

	// Cached: a second call should return the same value.
	if host.YardInfo() != info {
		t.Errorf("YardInfo should be cached and stable across calls")
	}
}

// TestYardInfoYardIDExplicit verifies that when cfg.YardID is set, it is
// used verbatim and the legacy Project fallback is NOT applied — even
// when Project is also set to a different value.
func TestYardInfoYardIDExplicit(t *testing.T) {
	cfg := &config.Config{
		Owner:   "zulandar",
		Project: "railyard",
		YardID:  "railyard-prod",
		Repo:    "https://github.com/zulandar/railyard",
	}
	host := NewHost(Dependencies{Cfg: cfg})

	info := host.YardInfo()
	if info.YardID != "railyard-prod" {
		t.Errorf("YardID = %q, want %q (explicit cfg.YardID)", info.YardID, "railyard-prod")
	}
	if info.Project != "railyard" {
		t.Errorf("Project = %q, want %q (unchanged)", info.Project, "railyard")
	}
}

// TestYardInfoYardIDFallback locks in the bit-for-bit compatibility
// promise: when cfg.YardID is empty, plugin.YardInfo.YardID is filled
// from cfg.Project. Without this, plugins like trainmaster lose their
// stable identifier on upgrade.
func TestYardInfoYardIDFallback(t *testing.T) {
	cfg := &config.Config{
		Owner:   "zulandar",
		Project: "railyard",
		// YardID intentionally empty.
	}
	host := NewHost(Dependencies{Cfg: cfg})

	info := host.YardInfo()
	if info.YardID != "railyard" {
		t.Errorf("YardID = %q, want %q (fallback to Project)", info.YardID, "railyard")
	}
}

// TestYardInfoYardIDEmptyWhenBothEmpty guards the no-config edge case:
// when both YardID and Project are empty (e.g. minimal test fixture),
// YardInfo.YardID stays empty rather than being filled with garbage.
func TestYardInfoYardIDEmptyWhenBothEmpty(t *testing.T) {
	cfg := &config.Config{Owner: "zulandar"}
	host := NewHost(Dependencies{Cfg: cfg})

	info := host.YardInfo()
	if info.YardID != "" {
		t.Errorf("YardID = %q, want empty when both YardID and Project are empty", info.YardID)
	}
}

// Legacy in-process lifecycle tests (TestLifecycleOrdering,
// TestInitFailureIsolation, TestStopDrainTimeout, TestRunDaemonInvokesFn)
// were removed when the subprocess go-plugin model replaced the in-process
// Plugin walk. They are tracked for re-write in bd issue railyard-bjp;
// see internal/pluginhost/launch_test.go for the new happy-path coverage
// of the subprocess launch + handshake.
