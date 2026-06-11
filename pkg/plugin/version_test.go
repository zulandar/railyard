package plugin

import "testing"

// TestSDKVersionNonEmpty pins that the SDK advertises a non-empty
// version string. The host surfaces this value in `ry plugins status`
// for support diagnostics (railyard-77h.8).
func TestSDKVersionNonEmpty(t *testing.T) {
	t.Parallel()
	if SDKVersion == "" {
		t.Fatal("SDKVersion must not be empty")
	}
}

// TestCoreEventTypesMatchesConstants asserts CoreEventTypes returns
// exactly the closed set of event topics declared in this package, so
// the host's Init-time topic advertisement (railyard-77h.8) cannot
// drift from the SDK constants.
func TestCoreEventTypesMatchesConstants(t *testing.T) {
	t.Parallel()
	want := []EventType{
		CarCreated,
		CarClaimed,
		CarStatusChanged,
		CarMerged,
		MergeFailed,
		EngineStarted,
		EngineStopped,
		EngineStalled,
		YardmasterAction,
		YardPaused,
		YardResumed,
	}
	got := CoreEventTypes()
	if len(got) != len(want) {
		t.Fatalf("CoreEventTypes len = %d, want %d (%v)", len(got), len(want), got)
	}
	seen := make(map[EventType]bool, len(got))
	for _, et := range got {
		seen[et] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("CoreEventTypes missing %q", w)
		}
	}
}
