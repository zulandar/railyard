package pluginhost

import (
	"context"
	"errors"
	"fmt"

	"github.com/zulandar/railyard/pkg/plugin"
)

// argSpec declares a required key in a [plugin.CommandArgs] map and the
// Go kind the value must satisfy. Validation in dispatchCommand consults
// this spec before invoking the bound function.
type argSpec struct {
	name string
	kind argKind
}

// argKind is the small, closed set of value shapes the host validates.
// Keeping it tiny on purpose: the allow-list (spec §7.3) only needs
// string + int, and any new commands should add a kind here rather than
// reaching for reflect.
type argKind int

const (
	argString argKind = iota
	argInt
)

// commandBinding is a single allow-list entry. Args declares the
// required argument schema; Fn is the implementation invoked once
// validation passes.
type commandBinding struct {
	args []argSpec
	fn   func(ctx context.Context, args plugin.CommandArgs) (plugin.CommandResult, error)
}

// buildAllowList constructs the static Phase 1 command allow-list (spec
// §7.3) wired against the function fields in Dependencies. The map is
// returned by value so callers (the *Host constructor) can store it once
// and never lock.
//
// Each binding's Fn closes over deps so the host package itself never
// imports the subsystem packages. When a Dependencies field is nil the
// binding returns Success=false with a clear "not wired" error; this
// keeps the OSS binary launchable without forcing every internal
// subsystem to be hooked up.
func buildAllowList(deps *Dependencies) map[string]commandBinding {
	return map[string]commandBinding{
		"pause_yard": {
			args: nil,
			fn: func(ctx context.Context, _ plugin.CommandArgs) (plugin.CommandResult, error) {
				if deps.PauseYardFn == nil {
					return notWired("pause_yard")
				}
				if err := deps.PauseYardFn(ctx, ""); err != nil {
					return plugin.CommandResult{Success: false, Error: err.Error()}, nil
				}
				return plugin.CommandResult{Success: true}, nil
			},
		},
		"resume_yard": {
			args: nil,
			fn: func(ctx context.Context, _ plugin.CommandArgs) (plugin.CommandResult, error) {
				if deps.ResumeYardFn == nil {
					return notWired("resume_yard")
				}
				if err := deps.ResumeYardFn(ctx, ""); err != nil {
					return plugin.CommandResult{Success: false, Error: err.Error()}, nil
				}
				return plugin.CommandResult{Success: true}, nil
			},
		},
		"reassign_car": {
			args: []argSpec{
				{name: "CarID", kind: argString},
				{name: "FromEngine", kind: argString},
			},
			fn: func(ctx context.Context, args plugin.CommandArgs) (plugin.CommandResult, error) {
				if deps.ReassignCarFn == nil {
					return notWired("reassign_car")
				}
				carID := args["CarID"].(string)
				fromEngine := args["FromEngine"].(string)
				if err := deps.ReassignCarFn(ctx, carID, fromEngine); err != nil {
					return plugin.CommandResult{Success: false, Error: err.Error()}, nil
				}
				return plugin.CommandResult{Success: true}, nil
			},
		},
		"scale_track": {
			args: []argSpec{
				{name: "Track", kind: argString},
				{name: "Count", kind: argInt},
			},
			fn: func(ctx context.Context, args plugin.CommandArgs) (plugin.CommandResult, error) {
				if deps.ScaleTrackFn == nil {
					return notWired("scale_track")
				}
				track := args["Track"].(string)
				count := coerceInt(args["Count"])
				if err := deps.ScaleTrackFn(ctx, track, count); err != nil {
					return plugin.CommandResult{Success: false, Error: err.Error()}, nil
				}
				return plugin.CommandResult{Success: true}, nil
			},
		},
		"force_complete": {
			args: []argSpec{
				{name: "CarID", kind: argString},
				{name: "Reason", kind: argString},
			},
			fn: func(ctx context.Context, args plugin.CommandArgs) (plugin.CommandResult, error) {
				if deps.ForceCompleteFn == nil {
					return notWired("force_complete")
				}
				carID := args["CarID"].(string)
				reason := args["Reason"].(string)
				if err := deps.ForceCompleteFn(ctx, carID, reason); err != nil {
					return plugin.CommandResult{Success: false, Error: err.Error()}, nil
				}
				return plugin.CommandResult{Success: true}, nil
			},
		},
	}
}

// notWired returns the standard "binding not connected" result. Lifted
// into a helper so every allow-list entry produces the same shape.
func notWired(name string) (plugin.CommandResult, error) {
	return plugin.CommandResult{
		Success: false,
		Error:   fmt.Sprintf("command %q is allow-listed but not wired in this binary", name),
	}, nil
}

// coerceInt accepts either an int or a float64 (JSON's default numeric
// type when args round-trip through map[string]any). Anything else has
// already been rejected by validateArgs before we reach this call.
func coerceInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// DispatchCommand is the in-process [plugin.Host] dispatch path. It
// resolves a name against (1) the static allow-list, then (2) the
// inProcCmds map populated by [Host.RegisterCommand]. The allow-list
// remains authoritative for the built-in names — RegisterCommand
// rejects any name that collides with it, so the fall-through can
// never override a core binding. Names absent from both maps return
// the standard "command not allowed" result.
//
// The fall-through INTENTIONALLY omits h.pluginCmds (the subprocess
// plugin-registered surface). Subprocess plugins are reached through
// hostservice.go DispatchCommand, which consults the allow-list and
// then pluginCmds in two explicit branches. The two surfaces are
// disjoint by design:
//
//   - In-process callers (core railyard subsystems / in-plugin SDK)
//     reach allow-list + inProcCmds via this method.
//   - Subprocess plugins reach allow-list + pluginCmds via the gRPC
//     HostService.DispatchCommand RPC.
//
// In-process invocation of a subprocess plugin's command would require
// driving its PluginService.HandleCommand RPC from here, which has no
// production caller today; if a future caller needs it, add the
// pluginCmds branch using lookupPluginByCommand + pluginRPC.HandleCommand
// mirroring hostservice.go.
func (h *Host) DispatchCommand(ctx context.Context, name string, args plugin.CommandArgs) (plugin.CommandResult, error) {
	if binding, ok := h.allowed[name]; ok {
		if err := validateArgs(binding.args, args); err != nil {
			return plugin.CommandResult{
				Success: false,
				Error:   err.Error(),
			}, nil
		}
		return binding.fn(ctx, args)
	}
	h.mu.RLock()
	handler, ok := h.inProcCmds[name]
	h.mu.RUnlock()
	if ok {
		return handler(ctx, args)
	}
	return plugin.CommandResult{
		Success: false,
		Error:   fmt.Sprintf("command not allowed: %s", name),
	}, nil
}

// RegisterCommand stores a plugin-provided command handler. Returns an
// error if the name conflicts with the allow-list or with a previously
// registered plugin command.
func (h *Host) RegisterCommand(name string, handler plugin.CommandHandler) error {
	if name == "" {
		return errors.New("pluginhost: command name must not be empty")
	}
	if handler == nil {
		return errors.New("pluginhost: command handler must not be nil")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.allowed[name]; exists {
		return fmt.Errorf("pluginhost: command %q conflicts with the core allow-list", name)
	}
	if _, exists := h.inProcCmds[name]; exists {
		return fmt.Errorf("pluginhost: command %q is already registered by another plugin", name)
	}
	if _, exists := h.pluginCmds[name]; exists {
		return fmt.Errorf("pluginhost: command %q is already registered by a subprocess plugin", name)
	}
	h.inProcCmds[name] = handler
	return nil
}

// validateArgs ensures every required key is present and carries a value
// of the declared kind. Returns nil when args satisfies the spec.
func validateArgs(specs []argSpec, args plugin.CommandArgs) error {
	for _, spec := range specs {
		raw, ok := args[spec.name]
		if !ok {
			return fmt.Errorf("missing required argument %q", spec.name)
		}
		if !matchesKind(raw, spec.kind) {
			return fmt.Errorf("argument %q has wrong type", spec.name)
		}
	}
	return nil
}

// matchesKind reports whether v satisfies the declared kind. int values
// accept Go ints and JSON-decoded float64s (which is what map[string]any
// payloads typically carry over the wire).
func matchesKind(v any, kind argKind) bool {
	switch kind {
	case argString:
		_, ok := v.(string)
		return ok
	case argInt:
		switch v.(type) {
		case int, int64, float64:
			return true
		}
		return false
	}
	return false
}
