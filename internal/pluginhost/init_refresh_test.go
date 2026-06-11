package pluginhost

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/zulandar/railyard/internal/config"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// TestHostInitRequestAdvertisesTopics proves the shared InitRequest builder
// always carries the host's supported event topics, so a crash-relaunch
// (railyard-uv8.7) re-negotiates topics exactly like the first boot rather
// than sending a bare request.
func TestHostInitRequestAdvertisesTopics(t *testing.T) {
	req := hostInitRequest("p")
	if req.PluginName != "p" {
		t.Errorf("PluginName = %q, want p", req.PluginName)
	}
	if req.Capabilities == nil {
		t.Error("Capabilities must be non-nil")
	}
	if !reflect.DeepEqual(req.SupportedEventTopics, canonicalEventTopics()) {
		t.Errorf("SupportedEventTopics = %v, want %v", req.SupportedEventTopics, canonicalEventTopics())
	}
}

// TestApplyInitResponseRefreshesCommandRegistry proves that applying a fresh
// InitResponse drops the plugin's previous command ownership + arg specs and
// re-registers from the response, so a relaunched binary that changed its
// command set cannot leave stale entries (railyard-uv8.7, compounding
// railyard-uv8.2). Another plugin's registrations are untouched.
func TestApplyInitResponseRefreshesCommandRegistry(t *testing.T) {
	h := &Host{
		allowed:    map[string]commandBinding{},
		pluginCmds: map[string]string{"old_cmd": "p", "keep": "other"},
		pluginCmdSpecs: map[string]*protov1.CommandSchema{
			"old_cmd": {Name: "old_cmd", Args: []*protov1.ArgSpec{{Name: "x", Type: protov1.ArgType_ARG_TYPE_STRING}}},
			"keep":    {Name: "keep"},
		},
	}
	lp := &launchedPlugin{
		name:   "p",
		logger: slog.Default(),
		allow:  newAllowList(config.AllowConfig{Events: []string{"*"}, Commands: []string{"*"}}),
	}

	resp := &protov1.InitResponse{
		SdkVersion:      "2.0.0",
		AllowedEvents:   []string{"CarCreated"},
		AllowedCommands: []string{"new_cmd"},
		CommandSpecs: []*protov1.CommandSchema{
			{Name: "new_cmd", Args: []*protov1.ArgSpec{{Name: "a", Type: protov1.ArgType_ARG_TYPE_INT, Required: true}}},
		},
	}

	h.applyInitResponse(lp, resp)

	// old_cmd ownership + spec gone; new_cmd registered; other plugin intact.
	if _, ok := h.pluginCmds["old_cmd"]; ok {
		t.Error("old_cmd ownership must be dropped on refresh")
	}
	if _, ok := h.pluginCmdSpecs["old_cmd"]; ok {
		t.Error("old_cmd spec must be dropped on refresh")
	}
	if owner := h.pluginCmds["new_cmd"]; owner != "p" {
		t.Errorf("new_cmd owner = %q, want p", owner)
	}
	if spec, ok := h.pluginCmdSpecs["new_cmd"]; !ok || len(spec.Args) != 1 || spec.Args[0].Type != protov1.ArgType_ARG_TYPE_INT {
		t.Errorf("new_cmd spec not refreshed: %+v", spec)
	}
	if h.pluginCmds["keep"] != "other" || h.pluginCmdSpecs["keep"] == nil {
		t.Error("another plugin's command ownership/spec must be untouched")
	}

	// Capabilities + sdkVersion reflect the fresh response.
	if lp.sdkVersion != "2.0.0" {
		t.Errorf("sdkVersion = %q, want 2.0.0", lp.sdkVersion)
	}
	if !reflect.DeepEqual(lp.capabilities.provideCommands, []string{"new_cmd"}) {
		t.Errorf("provideCommands = %v, want [new_cmd]", lp.capabilities.provideCommands)
	}
	if !reflect.DeepEqual(lp.capabilities.subscribeEvents, []string{"CarCreated"}) {
		t.Errorf("subscribeEvents = %v, want [CarCreated]", lp.capabilities.subscribeEvents)
	}
}
