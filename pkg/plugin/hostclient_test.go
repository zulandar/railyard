package plugin

import (
	"context"
	"testing"
)

// TestHostClientUnknownTopic covers the Init-time topic-negotiation
// helper (railyard-77h.8): once the host advertises a non-empty set of
// supported topics, a Subscribe to a topic outside that set is flagged
// as unknown. A host that advertises NOTHING (an old host that predates
// negotiation) disables the check entirely so new plugins keep working
// against old hosts.
func TestHostClientUnknownTopic(t *testing.T) {
	t.Parallel()

	t.Run("negotiated host flags unknown topics", func(t *testing.T) {
		t.Parallel()
		hc := &hostClient{
			rootCtx:         context.Background(),
			commandHandlers: make(map[string]CommandHandler),
		}
		hc.setSupportedTopics([]string{string(CarCreated), string(EngineStalled)})
		if hc.unknownTopic(string(CarCreated)) {
			t.Error("advertised topic must not be flagged unknown")
		}
		if hc.unknownTopic(string(EngineStalled)) {
			t.Error("advertised topic must not be flagged unknown")
		}
		if !hc.unknownTopic("trainmaster.remote_changed") {
			t.Error("un-advertised topic must be flagged unknown")
		}
	})

	t.Run("old host with empty advertisement disables the check", func(t *testing.T) {
		t.Parallel()
		hc := &hostClient{
			rootCtx:         context.Background(),
			commandHandlers: make(map[string]CommandHandler),
		}
		hc.setSupportedTopics(nil)
		if hc.unknownTopic(string(CarCreated)) {
			t.Error("empty advertisement must disable the unknown-topic check")
		}
		if hc.unknownTopic("anything-at-all") {
			t.Error("empty advertisement must disable the unknown-topic check")
		}
	})
}
