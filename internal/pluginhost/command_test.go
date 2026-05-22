package pluginhost

import (
	"context"
	"errors"
	"testing"

	"github.com/zulandar/railyard/pkg/plugin"
)

// TestDispatchUnknownCommand verifies an unknown command name returns
// Success=false with the documented error string and does NOT propagate
// the failure as a Go error (validation failures are in-band per spec).
func TestDispatchUnknownCommand(t *testing.T) {
	host := NewHost(Dependencies{})
	res, err := host.DispatchCommand(context.Background(), "nope", nil)
	if err != nil {
		t.Fatalf("unknown command should not return error, got %v", err)
	}
	if res.Success {
		t.Errorf("Success = true, want false")
	}
	if res.Error == "" || res.Error[:len("command not allowed")] != "command not allowed" {
		t.Errorf("Error = %q, want prefix 'command not allowed'", res.Error)
	}
}

// TestDispatchAllowListedCommand exercises the happy path: the binding's
// Fn is invoked when args satisfy the schema.
func TestDispatchAllowListedCommand(t *testing.T) {
	var seenCar, seenEng string
	host := NewHost(Dependencies{
		ReassignCarFn: func(ctx context.Context, carID, fromEngine string) error {
			seenCar, seenEng = carID, fromEngine
			return nil
		},
	})
	args := plugin.CommandArgs{
		"CarID":      "car-123",
		"FromEngine": "eng-7",
	}
	res, err := host.DispatchCommand(context.Background(), "reassign_car", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Fatalf("Success = false (Error=%q), want true", res.Error)
	}
	if seenCar != "car-123" || seenEng != "eng-7" {
		t.Errorf("binding saw (%q,%q), want (car-123, eng-7)", seenCar, seenEng)
	}
}

// TestDispatchMissingArg checks the validation error path: a required
// arg is missing → Success=false, helpful Error string.
func TestDispatchMissingArg(t *testing.T) {
	host := NewHost(Dependencies{
		ReassignCarFn: func(ctx context.Context, carID, fromEngine string) error { return nil },
	})
	args := plugin.CommandArgs{"CarID": "car-123"} // FromEngine missing
	res, err := host.DispatchCommand(context.Background(), "reassign_car", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Error("Success = true, want false on missing required arg")
	}
	if res.Error == "" {
		t.Error("Error string should describe the missing arg")
	}
}

// TestDispatchWrongType verifies type validation: passing an int where
// a string is required → Success=false.
func TestDispatchWrongType(t *testing.T) {
	host := NewHost(Dependencies{
		ReassignCarFn: func(ctx context.Context, carID, fromEngine string) error { return nil },
	})
	args := plugin.CommandArgs{"CarID": 42, "FromEngine": "eng"} // CarID is int, expected string
	res, err := host.DispatchCommand(context.Background(), "reassign_car", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Error("Success = true, want false on wrong arg type")
	}
}

// TestDispatchIntFromFloat verifies a JSON-decoded numeric (float64) is
// accepted where an int kind is required.
func TestDispatchIntFromFloat(t *testing.T) {
	var seenCount int
	host := NewHost(Dependencies{
		ScaleTrackFn: func(ctx context.Context, track string, count int) error {
			seenCount = count
			return nil
		},
	})
	args := plugin.CommandArgs{"Track": "go", "Count": float64(5)}
	res, err := host.DispatchCommand(context.Background(), "scale_track", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Fatalf("Success = false (Error=%q)", res.Error)
	}
	if seenCount != 5 {
		t.Errorf("count = %d, want 5", seenCount)
	}
}

// TestDispatchBindingNotWired confirms an allow-listed command whose
// dependency Fn is nil returns Success=false with the "not wired"
// message rather than dereferencing nil.
func TestDispatchBindingNotWired(t *testing.T) {
	host := NewHost(Dependencies{})
	res, err := host.DispatchCommand(context.Background(), "pause_yard", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Error("Success = true, want false when binding is not wired")
	}
	if res.Error == "" {
		t.Error("Error should describe the missing wiring")
	}
}

// TestDispatchBindingError propagates the underlying function's error
// into the Result's Error field with Success=false.
func TestDispatchBindingError(t *testing.T) {
	host := NewHost(Dependencies{
		ForceCompleteFn: func(ctx context.Context, carID, reason string) error {
			return errors.New("car already merged")
		},
	})
	args := plugin.CommandArgs{"CarID": "c1", "Reason": "operator override"}
	res, _ := host.DispatchCommand(context.Background(), "force_complete", args)
	if res.Success {
		t.Error("Success = true, want false when binding errors")
	}
	if res.Error != "car already merged" {
		t.Errorf("Error = %q, want %q", res.Error, "car already merged")
	}
}

// TestRegisterCommandConflictsWithAllowList rejects names that overlap
// the allow-list.
func TestRegisterCommandConflictsWithAllowList(t *testing.T) {
	host := NewHost(Dependencies{})
	err := host.RegisterCommand("pause_yard", func(ctx context.Context, args plugin.CommandArgs) (plugin.CommandResult, error) {
		return plugin.CommandResult{Success: true}, nil
	})
	if err == nil {
		t.Fatal("RegisterCommand should reject names that collide with the allow-list")
	}
}

// TestRegisterCommandDuplicate rejects double-registration of the same
// plugin-provided name.
func TestRegisterCommandDuplicate(t *testing.T) {
	host := NewHost(Dependencies{})
	noop := func(ctx context.Context, args plugin.CommandArgs) (plugin.CommandResult, error) {
		return plugin.CommandResult{Success: true}, nil
	}
	if err := host.RegisterCommand("my.cmd", noop); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := host.RegisterCommand("my.cmd", noop); err == nil {
		t.Error("second registration should fail with a conflict error")
	}
}

// TestRegisterCommandValidation rejects empty names and nil handlers.
func TestRegisterCommandValidation(t *testing.T) {
	host := NewHost(Dependencies{})
	if err := host.RegisterCommand("", nil); err == nil {
		t.Error("empty name should error")
	}
	if err := host.RegisterCommand("ok", nil); err == nil {
		t.Error("nil handler should error")
	}
}

// TestDispatchPluginRegisteredCommand verifies that a command registered
// via Host.RegisterCommand is reachable through Host.DispatchCommand when
// the name is not in the core allow-list. The handler's return value is
// propagated verbatim.
func TestDispatchPluginRegisteredCommand(t *testing.T) {
	host := NewHost(Dependencies{})
	var gotCtx context.Context
	var gotArgs plugin.CommandArgs
	want := plugin.CommandResult{Success: true, Data: map[string]any{"echo": "hi"}}
	handler := func(ctx context.Context, args plugin.CommandArgs) (plugin.CommandResult, error) {
		gotCtx, gotArgs = ctx, args
		return want, nil
	}
	if err := host.RegisterCommand("my.echo", handler); err != nil {
		t.Fatalf("RegisterCommand: %v", err)
	}
	ctx := context.Background()
	args := plugin.CommandArgs{"msg": "hi"}
	res, err := host.DispatchCommand(ctx, "my.echo", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Fatalf("Success = false (Error=%q), want true", res.Error)
	}
	if gotCtx != ctx {
		t.Errorf("handler ctx not propagated")
	}
	if gotArgs["msg"] != "hi" {
		t.Errorf("handler args = %v, want msg=hi", gotArgs)
	}
	if v, _ := res.Data["echo"].(string); v != "hi" {
		t.Errorf("Data.echo = %v, want hi", res.Data["echo"])
	}
}

// TestDispatchPluginRegisteredCommandHandlerError verifies a plugin
// handler's returned Go error is surfaced verbatim by DispatchCommand
// (not wrapped) — same shape callers see for allow-list bindings.
func TestDispatchPluginRegisteredCommandHandlerError(t *testing.T) {
	host := NewHost(Dependencies{})
	boom := errors.New("boom")
	handler := func(ctx context.Context, args plugin.CommandArgs) (plugin.CommandResult, error) {
		return plugin.CommandResult{Success: false, Error: boom.Error()}, boom
	}
	if err := host.RegisterCommand("my.boom", handler); err != nil {
		t.Fatalf("RegisterCommand: %v", err)
	}
	res, err := host.DispatchCommand(context.Background(), "my.boom", nil)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
	if res.Success {
		t.Error("Success = true, want false")
	}
	if res.Error != "boom" {
		t.Errorf("Error = %q, want %q", res.Error, "boom")
	}
}

// TestRegisterCommandStillRejectsAllowListAfterFallThrough re-asserts the
// post-change behavior: RegisterCommand must still reject names that
// collide with the core allow-list, AND DispatchCommand for that name
// must continue to hit the core binding (not the plugin map). This
// preserves the security boundary the allow-list provides.
func TestRegisterCommandStillRejectsAllowListAfterFallThrough(t *testing.T) {
	var called bool
	host := NewHost(Dependencies{
		PauseYardFn: func(ctx context.Context, _ string) error {
			called = true
			return nil
		},
	})
	err := host.RegisterCommand("pause_yard", func(ctx context.Context, args plugin.CommandArgs) (plugin.CommandResult, error) {
		t.Fatal("plugin handler must never be invoked for an allow-list name")
		return plugin.CommandResult{}, nil
	})
	if err == nil {
		t.Fatal("RegisterCommand should reject names that collide with the allow-list")
	}
	res, derr := host.DispatchCommand(context.Background(), "pause_yard", nil)
	if derr != nil {
		t.Fatalf("DispatchCommand: %v", derr)
	}
	if !res.Success {
		t.Fatalf("Success = false (Error=%q), want true via core binding", res.Error)
	}
	if !called {
		t.Error("core PauseYardFn should have been invoked")
	}
}
