//go:build linux
// +build linux

package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// TestCommandSpecRoundTripE2E proves the full host-validated command path
// across a real subprocess (railyard-77h.16): the plugin registers a typed
// command via RegisterCommandSpec, the host stores the reported spec from
// InitResponse.command_specs, validates dispatched args against it, and
// forwards valid args to the plugin's handler. It then asserts that:
//
//   - the command's signature is surfaced in Status() (CLI -v rendering),
//   - valid args reach the handler (recorded in the plugin's log),
//   - a wrong-typed arg is rejected host-side WITHOUT reaching the plugin.
func TestCommandSpecRoundTripE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess; skipped under -short")
	}

	bin := buildTestPlugin(t)
	pluginsDir := t.TempDir()
	dst := filepath.Join(pluginsDir, "testplugin")
	if err := copyExec(bin, dst); err != nil {
		t.Fatalf("copy binary: %v", err)
	}

	cmdLog := filepath.Join(t.TempDir(), "cmd.log")
	t.Setenv("RAILYARD_TESTPLUGIN_CMD", "1")
	t.Setenv("RAILYARD_TESTPLUGIN_CMD_LOG", cmdLog)

	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled:    []string{"testplugin"},
			PluginsDir: pluginsDir,
			Settings: map[string]config.PluginSettings{
				"testplugin": {Allow: config.AllowConfig{
					Commands: []string{"testplugin.*"},
				}},
			},
		},
	}
	bus := events.NewBus()
	t.Cleanup(func() {
		if c, ok := bus.(interface{ Close() }); ok {
			c.Close()
		}
	})
	host := NewHost(Dependencies{Cfg: cfg, Bus: bus, RailyardVersion: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host.Init(ctx)
	if names := host.Names(); len(names) != 1 {
		t.Fatalf("expected one launched plugin, got %v", names)
	}
	host.Start(ctx)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		host.Stop(stopCtx)
	})

	// The host should have stored the typed spec the plugin reported in its
	// InitResponse, and Status() should surface the signature.
	if spec := host.lookupPluginCmdSpec("testplugin.scale"); spec == nil {
		t.Fatal("host did not store the plugin's reported command spec")
	}
	snap := host.Status()
	var sigSeen bool
	for _, p := range snap.Plugins {
		for _, s := range p.CommandSignatures {
			if s == "testplugin.scale(Track:string, Count:int)" {
				sigSeen = true
			}
		}
	}
	if !sigSeen {
		t.Errorf("command signature not surfaced in Status(): %+v", snap.Plugins)
	}

	hs := newHostService(host, "testplugin")

	// Valid args reach the handler.
	validArgs, err := structpb.NewStruct(map[string]any{"Track": "backend", "Count": 5})
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	resp, err := hs.DispatchCommand(ctx, &protov1.DispatchCommandRequest{
		Name: "testplugin.scale",
		Args: validArgs,
	})
	if err != nil {
		t.Fatalf("DispatchCommand(valid): %v", err)
	}
	if !resp.Success {
		t.Fatalf("valid dispatch failed: %s", resp.Error)
	}
	// The handler logs the invocation; wait for it to land.
	deadline := time.Now().Add(10 * time.Second)
	handled := false
	for time.Now().Before(deadline) {
		if data, rerr := os.ReadFile(cmdLog); rerr == nil && strings.Contains(string(data), "handled Track=backend Count=5") {
			handled = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !handled {
		data, _ := os.ReadFile(cmdLog)
		t.Fatalf("plugin handler never recorded the valid invocation; cmd log:\n%s", string(data))
	}

	// A wrong-typed arg is rejected host-side WITHOUT reaching the plugin:
	// the handler appends one line per invocation, so the log must still
	// contain exactly one line after a rejected dispatch.
	badArgs, err := structpb.NewStruct(map[string]any{"Track": "backend", "Count": "five"})
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	badResp, err := hs.DispatchCommand(ctx, &protov1.DispatchCommandRequest{
		Name: "testplugin.scale",
		Args: badArgs,
	})
	if err != nil {
		t.Fatalf("DispatchCommand(invalid): unexpected gRPC error: %v", err)
	}
	if badResp.Success {
		t.Error("wrong-typed dispatch returned Success=true, want false")
	}
	if badResp.Error == "" {
		t.Error("expected a validation error message")
	}
	// Give any (erroneously issued) RPC a moment to land, then confirm the
	// handler was not invoked a second time.
	time.Sleep(300 * time.Millisecond)
	data, _ := os.ReadFile(cmdLog)
	if got := strings.Count(string(data), "handled "); got != 1 {
		t.Errorf("handler invoked %d times, want 1 (invalid args must not reach the plugin); log:\n%s", got, string(data))
	}
}
