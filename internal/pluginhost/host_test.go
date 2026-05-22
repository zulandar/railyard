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

	// Cached: a second call should return the same value.
	if host.YardInfo() != info {
		t.Errorf("YardInfo should be cached and stable across calls")
	}
}

// Legacy in-process lifecycle tests (TestLifecycleOrdering,
// TestInitFailureIsolation, TestStopDrainTimeout, TestRunDaemonInvokesFn)
// were removed when railyard-fll.3 swapped the in-process Plugin walk for
// the subprocess go-plugin model. They are tracked for re-write in bd
// issue railyard-bjp; see internal/pluginhost/launch_test.go for the new
// happy-path coverage of the subprocess launch + handshake.
