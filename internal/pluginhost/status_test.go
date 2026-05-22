package pluginhost

import (
	"testing"
	"time"
)

func TestInitFailuresRetainedOnInitError(t *testing.T) {
	// Construct a host with a fake initFailure already in place to
	// confirm Status() can see it. (We test the full Init pipeline
	// via the integration test in Task 11; here we just verify the
	// data structure is wired.)
	h := &Host{
		initFailures: map[string]initFailure{
			"broken": {
				name:     "broken",
				path:     "/etc/railyard/plugins.d/broken",
				err:      "init: yard not ready",
				failedAt: time.Unix(2000, 0),
			},
		},
		launched: map[string]*launchedPlugin{},
		bootedAt: time.Unix(1000, 0),
	}
	snap := h.Status()
	// (Status not yet implemented — this test currently asserts the
	// type compiles. Full assertions land in Task 6.)
	_ = snap
}

func TestSkippedRetainedFromInit(t *testing.T) {
	h := &Host{
		skipped: []skippedPlugin{
			{name: "missing-plugin", searched: []string{"/etc/railyard/plugins.d", "~/.railyard/plugins"}},
		},
		initFailures: map[string]initFailure{},
		launched:     map[string]*launchedPlugin{},
		bootedAt:     time.Unix(1000, 0),
	}
	snap := h.Status()
	_ = snap
}
