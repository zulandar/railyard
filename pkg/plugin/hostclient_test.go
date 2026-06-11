package plugin

import (
	"context"
	"testing"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
	"google.golang.org/protobuf/types/known/structpb"
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
		if !hc.unknownTopic("Frobnicate") {
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

	t.Run("namespaced plugin topics are never flagged unknown", func(t *testing.T) {
		t.Parallel()
		hc := &hostClient{
			rootCtx:         context.Background(),
			commandHandlers: make(map[string]CommandHandler),
		}
		// Host advertises only the core topics; a plugin-published
		// dynamic topic ("<plugin>.<name>") is legitimately absent from
		// that list and MUST NOT be flagged (railyard-77h.9 interaction
		// with the 77h.8 negotiation check).
		hc.setSupportedTopics([]string{string(CarCreated)})
		if hc.unknownTopic("trainmaster.synced") {
			t.Error("namespaced plugin topic must not be flagged unknown")
		}
	})
}

// TestHostClientRegisterCommandSpec covers the SDK registration surface
// for typed command schemas (railyard-77h.16). A command registered via
// RegisterCommandSpec is advertised both as a name AND as a wire
// CommandSchema with its typed args; a bare RegisterCommand is advertised
// only as a name and contributes no spec.
func TestHostClientRegisterCommandSpec(t *testing.T) {
	t.Parallel()

	hc := &hostClient{
		rootCtx:         context.Background(),
		commandHandlers: make(map[string]CommandHandler),
		commandSpecs:    make(map[string]CommandSpec),
	}
	handler := func(context.Context, CommandArgs) (CommandResult, error) {
		return CommandResult{Success: true}, nil
	}

	spec := CommandSpec{
		Name: "scale",
		Args: []ArgSpec{
			{Name: "Track", Type: ArgString, Required: true},
			{Name: "Count", Type: ArgInt, Required: true},
			{Name: "Force", Type: ArgBool, Required: false, Description: "skip safety checks"},
		},
	}
	if err := hc.RegisterCommandSpec(spec, handler); err != nil {
		t.Fatalf("RegisterCommandSpec: %v", err)
	}
	// A bare command shares the name space but contributes no spec.
	if err := hc.RegisterCommand("bare", handler); err != nil {
		t.Fatalf("RegisterCommand: %v", err)
	}

	// Both names are advertised.
	names := hc.advertisedCommandNames()
	if len(names) != 2 {
		t.Fatalf("advertisedCommandNames = %v, want 2 entries", names)
	}

	// Exactly one spec is advertised, and it round-trips the typed args.
	specs := hc.advertisedCommandSpecs()
	if len(specs) != 1 {
		t.Fatalf("advertisedCommandSpecs = %d, want 1 (bare command contributes none)", len(specs))
	}
	got := specs[0]
	if got.Name != "scale" {
		t.Errorf("spec name = %q, want scale", got.Name)
	}
	if len(got.Args) != 3 {
		t.Fatalf("spec args = %d, want 3", len(got.Args))
	}
	want := []struct {
		name string
		typ  protov1.ArgType
		req  bool
	}{
		{"Track", protov1.ArgType_ARG_TYPE_STRING, true},
		{"Count", protov1.ArgType_ARG_TYPE_INT, true},
		{"Force", protov1.ArgType_ARG_TYPE_BOOL, false},
	}
	for i, w := range want {
		a := got.Args[i]
		if a.Name != w.name || a.Type != w.typ || a.Required != w.req {
			t.Errorf("arg[%d] = {%q %v %v}, want {%q %v %v}", i, a.Name, a.Type, a.Required, w.name, w.typ, w.req)
		}
	}

	// Re-registering a name (bare or typed) is rejected.
	if err := hc.RegisterCommandSpec(spec, handler); err == nil {
		t.Error("RegisterCommandSpec on a duplicate name must error")
	}
	if err := hc.RegisterCommandSpec(CommandSpec{}, handler); err == nil {
		t.Error("RegisterCommandSpec with empty name must error")
	}
	if err := hc.RegisterCommandSpec(CommandSpec{Name: "x"}, nil); err == nil {
		t.Error("RegisterCommandSpec with nil handler must error")
	}
}

// TestDecodeEventCustom decodes a plugin-published dynamic event: the
// custom Struct arm + topic_name become a map[string]any payload under
// the namespaced EventType (railyard-77h.9).
func TestDecodeEventCustom(t *testing.T) {
	t.Parallel()

	st, err := structpb.NewStruct(map[string]any{"hello": "world", "n": float64(3)})
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	ev := &protov1.Event{
		Type:      protov1.EventType_EVENT_TYPE_UNSPECIFIED,
		TopicName: "trainmaster.synced",
		Payload:   &protov1.Event_Custom{Custom: st},
	}
	decoded, err := decodeEvent(ev)
	if err != nil {
		t.Fatalf("decodeEvent: %v", err)
	}
	if decoded.topic != EventType("trainmaster.synced") {
		t.Errorf("topic = %q, want trainmaster.synced", decoded.topic)
	}
	m, ok := decoded.payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]any", decoded.payload)
	}
	if m["hello"] != "world" || m["n"] != float64(3) {
		t.Errorf("payload = %v, want hello=world n=3", m)
	}
}
