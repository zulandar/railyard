package plugintest_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/zulandar/railyard/pkg/plugin"
	"github.com/zulandar/railyard/pkg/plugin/plugintest"
)

// TestFakeHostSatisfiesInterface is the runtime mirror of the
// compile-time `var _ plugin.Host = (*FakeHost)(nil)` assertion. The
// test serves as documentation: a reader scanning *_test.go sees the
// contract spelled out explicitly. The compile-time check is what
// actually enforces it.
func TestFakeHostSatisfiesInterface(t *testing.T) {
	var _ plugin.Host = (*plugintest.FakeHost)(nil)
}

func TestConfigReturnsConfiguredNode(t *testing.T) {
	t.Parallel()

	node := plugintest.MustYAMLNode(`enabled: true`)
	fh := &plugintest.FakeHost{
		ConfigValues: map[string]yaml.Node{"my-plugin": node},
	}

	got := fh.Config("my-plugin")
	if got.Kind != yaml.MappingNode {
		t.Fatalf("expected MappingNode, got Kind=%d", got.Kind)
	}

	missing := fh.Config("unknown-plugin")
	if missing.Kind != 0 {
		t.Fatalf("expected zero-value node for missing key, got Kind=%d", missing.Kind)
	}
}

func TestConfigZeroValueWhenMapNil(t *testing.T) {
	t.Parallel()

	fh := &plugintest.FakeHost{}
	got := fh.Config("anything")
	if got.Kind != 0 {
		t.Fatalf("expected zero-value node when ConfigValues nil, got Kind=%d", got.Kind)
	}
}

func TestYardInfoReturnsConfiguredValue(t *testing.T) {
	t.Parallel()

	want := plugin.YardInfo{YardID: "test-yard", Owner: "zulandar"}
	fh := &plugintest.FakeHost{YardInfoValue: want}

	got := fh.YardInfo()
	if got != want {
		t.Fatalf("YardInfo mismatch: got %+v want %+v", got, want)
	}
}

func TestSnapshotReturnsConfiguredValue(t *testing.T) {
	t.Parallel()

	want := &plugin.Snapshot{Cars: plugin.CarsSnap{Counts: map[string]int{"open": 3}}}
	fh := &plugintest.FakeHost{SnapshotValue: want}

	got, err := fh.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("Snapshot did not return the configured pointer")
	}
}

func TestSnapshotReturnsConfiguredError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("boom")
	fh := &plugintest.FakeHost{SnapshotErr: sentinel}

	_, err := fh.Snapshot(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

func TestSubscribeRecordsAndUnsubscribe(t *testing.T) {
	t.Parallel()

	fh := &plugintest.FakeHost{}
	called := 0
	unsub := fh.Subscribe(plugin.CarCreated, func(_ plugin.EventType, _ any) {
		called++
	})

	subs := fh.Subscriptions()
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}
	if subs[0].Topic != plugin.CarCreated {
		t.Fatalf("expected topic %q, got %q", plugin.CarCreated, subs[0].Topic)
	}
	if subs[0].Unsubscribed {
		t.Fatalf("expected new subscription not to be unsubscribed")
	}

	unsub()
	unsub() // safe to call twice

	subs = fh.Subscriptions()
	if !subs[0].Unsubscribed {
		t.Fatalf("expected subscription to be marked unsubscribed after unsub()")
	}
}

func TestDriveEventInvokesMatchingHandlers(t *testing.T) {
	t.Parallel()

	fh := &plugintest.FakeHost{}
	var carCreatedSeen, carClaimedSeen int

	fh.Subscribe(plugin.CarCreated, func(_ plugin.EventType, payload any) {
		evt, ok := payload.(plugin.CarCreatedEvent)
		if !ok {
			t.Errorf("expected CarCreatedEvent, got %T", payload)
			return
		}
		if evt.CarID != "c-1" {
			t.Errorf("expected CarID c-1, got %q", evt.CarID)
		}
		carCreatedSeen++
	})
	fh.Subscribe(plugin.CarClaimed, func(_ plugin.EventType, _ any) {
		carClaimedSeen++
	})

	n := fh.DriveEvent(plugin.CarCreated, plugin.CarCreatedEvent{CarID: "c-1"})
	if n != 1 {
		t.Fatalf("expected 1 handler invoked, got %d", n)
	}
	if carCreatedSeen != 1 {
		t.Fatalf("expected CarCreated handler to fire once, got %d", carCreatedSeen)
	}
	if carClaimedSeen != 0 {
		t.Fatalf("CarClaimed handler should not have fired; got %d", carClaimedSeen)
	}
}

func TestDriveEventSkipsUnsubscribedHandlers(t *testing.T) {
	t.Parallel()

	fh := &plugintest.FakeHost{}
	count := 0
	unsub := fh.Subscribe(plugin.CarCreated, func(_ plugin.EventType, _ any) { count++ })
	unsub()

	n := fh.DriveEvent(plugin.CarCreated, plugin.CarCreatedEvent{})
	if n != 0 {
		t.Fatalf("expected 0 handlers invoked after unsubscribe, got %d", n)
	}
	if count != 0 {
		t.Fatalf("expected handler not to fire; got %d", count)
	}
}

func TestRegisterCommandRecords(t *testing.T) {
	t.Parallel()

	fh := &plugintest.FakeHost{}
	handler := func(_ context.Context, _ plugin.CommandArgs) (plugin.CommandResult, error) {
		return plugin.CommandResult{Success: true}, nil
	}

	if err := fh.RegisterCommand("do_thing", handler); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	regs := fh.Registrations()
	if len(regs) != 1 {
		t.Fatalf("expected 1 registration, got %d", len(regs))
	}
	if regs[0].Name != "do_thing" {
		t.Fatalf("expected name do_thing, got %q", regs[0].Name)
	}
	if regs[0].Handler == nil {
		t.Fatalf("expected handler to be retained")
	}
}

func TestRegisterCommandReturnsConfiguredError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("conflict")
	fh := &plugintest.FakeHost{RegisterCommandErr: sentinel}

	err := fh.RegisterCommand("clashes", nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	// Registration is still recorded so tests can inspect the attempted name.
	regs := fh.Registrations()
	if len(regs) != 1 {
		t.Fatalf("expected 1 registration even on error, got %d", len(regs))
	}
}

func TestDispatchCommandRoutesToHandler(t *testing.T) {
	t.Parallel()

	fh := &plugintest.FakeHost{
		DispatchHandlers: map[string]plugin.CommandHandler{
			"force_complete": func(_ context.Context, args plugin.CommandArgs) (plugin.CommandResult, error) {
				return plugin.CommandResult{
					Success: true,
					Data:    map[string]any{"car_id": args["car_id"]},
				}, nil
			},
		},
	}

	res, err := fh.DispatchCommand(context.Background(), "force_complete", plugin.CommandArgs{"car_id": "c-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected Success=true, got %+v", res)
	}
	if got := res.Data["car_id"]; got != "c-1" {
		t.Fatalf("expected car_id c-1, got %v", got)
	}

	disps := fh.Dispatches()
	if len(disps) != 1 || disps[0].Name != "force_complete" {
		t.Fatalf("expected one recorded dispatch for force_complete, got %+v", disps)
	}
}

func TestDispatchCommandWithoutHandler(t *testing.T) {
	t.Parallel()

	fh := &plugintest.FakeHost{}
	res, err := fh.DispatchCommand(context.Background(), "unknown", nil)
	if err == nil {
		t.Fatalf("expected error for missing handler, got nil")
	}
	if res.Success {
		t.Fatalf("expected Success=false for missing handler, got %+v", res)
	}
	if res.Error == "" {
		t.Fatalf("expected non-empty Error string")
	}
	// The call is still recorded so tests can assert "the plugin tried".
	if len(fh.Dispatches()) != 1 {
		t.Fatalf("expected dispatch to be recorded even when no handler matched")
	}
}

func TestLoggerCapturesRecords(t *testing.T) {
	t.Parallel()

	fh := &plugintest.FakeHost{}
	log := fh.Logger()
	log.Info("hello", "k", "v", "n", 42)
	log.Warn("careful")

	logs := fh.Logs()
	if len(logs) != 2 {
		t.Fatalf("expected 2 log records, got %d", len(logs))
	}
	if logs[0].Level != slog.LevelInfo {
		t.Fatalf("first record: expected Info, got %v", logs[0].Level)
	}
	if logs[0].Message != "hello" {
		t.Fatalf("first record: expected message 'hello', got %q", logs[0].Message)
	}
	// Walk attrs to confirm both k=v and n=42 made it through.
	saw := map[string]any{}
	for _, a := range logs[0].Attrs {
		saw[a.Key] = a.Value.Any()
	}
	if saw["k"] != "v" {
		t.Fatalf("expected attr k=v, got %v", saw["k"])
	}
	if saw["n"] != int64(42) {
		t.Fatalf("expected attr n=42, got %v (%T)", saw["n"], saw["n"])
	}

	if logs[1].Level != slog.LevelWarn {
		t.Fatalf("second record: expected Warn, got %v", logs[1].Level)
	}
}

func TestLoggerWithAttrsPropagates(t *testing.T) {
	t.Parallel()

	fh := &plugintest.FakeHost{}
	log := fh.Logger().With("plugin", "myplugin")
	log.Info("scoped")

	logs := fh.Logs()
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	saw := map[string]any{}
	for _, a := range logs[0].Attrs {
		saw[a.Key] = a.Value.Any()
	}
	if saw["plugin"] != "myplugin" {
		t.Fatalf("expected With() attr to propagate, got %+v", saw)
	}
}

func TestResetClearsRecordings(t *testing.T) {
	t.Parallel()

	fh := &plugintest.FakeHost{}
	fh.Subscribe(plugin.CarCreated, func(_ plugin.EventType, _ any) {})
	_ = fh.RegisterCommand("noop", nil)
	fh.Logger().Info("msg")
	_, _ = fh.DispatchCommand(context.Background(), "noop", nil)

	fh.Reset()

	if len(fh.Subscriptions()) != 0 {
		t.Fatalf("expected Subscriptions cleared")
	}
	if len(fh.Registrations()) != 0 {
		t.Fatalf("expected Registrations cleared")
	}
	if len(fh.Dispatches()) != 0 {
		t.Fatalf("expected Dispatches cleared")
	}
	if len(fh.Logs()) != 0 {
		t.Fatalf("expected Logs cleared")
	}
}

func TestMustYAMLNodeParses(t *testing.T) {
	t.Parallel()

	node := plugintest.MustYAMLNode("foo: bar")
	if node.Kind != yaml.MappingNode {
		t.Fatalf("expected MappingNode, got Kind=%d", node.Kind)
	}
}

func TestMustYAMLNodePanicsOnInvalid(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on invalid YAML")
		}
	}()
	plugintest.MustYAMLNode("foo: [unterminated")
}
