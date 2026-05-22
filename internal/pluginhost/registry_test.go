package pluginhost

import (
	"reflect"
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

func TestResolveAllowList_NoCfg(t *testing.T) {
	h := NewHost(Dependencies{})
	got := h.resolveAllowList("p1")
	if !got.IsEmpty() {
		t.Errorf("expected empty allow-list with nil cfg, got %+v", got)
	}
}

func TestResolveAllowList_NoEntry(t *testing.T) {
	h := NewHost(Dependencies{Cfg: &config.Config{
		Plugins: config.PluginsConfig{Enabled: []string{"p1"}},
	}})
	got := h.resolveAllowList("p1")
	if !got.IsEmpty() {
		t.Errorf("expected empty allow-list, got %+v", got)
	}
}

func TestResolveAllowList_FromConfig(t *testing.T) {
	h := NewHost(Dependencies{Cfg: &config.Config{
		Plugins: config.PluginsConfig{
			Enabled: []string{"p1"},
			Settings: map[string]config.PluginSettings{
				"p1": {Allow: config.AllowConfig{
					Events:   []string{"CarCreated"},
					Commands: []string{"foo.*"},
				}},
			},
		},
	}})
	got := h.resolveAllowList("p1")
	if !got.AllowEvent("CarCreated") {
		t.Error("AllowEvent(CarCreated) = false, want true")
	}
	if got.AllowEvent("MergeFailed") {
		t.Error("AllowEvent(MergeFailed) = true, want false")
	}
	if !got.AllowCommand("foo.bar") {
		t.Error("AllowCommand(foo.bar) = false, want true")
	}
}

func TestFilterAllowedEvents(t *testing.T) {
	allow := newAllowList(config.AllowConfig{Events: []string{"CarCreated", "MergeFailed"}})
	advertised := []string{"CarCreated", "CarMerged", "MergeFailed", "EngineStalled"}
	allowed, denied := filterAllowedEvents(advertised, allow)
	if !reflect.DeepEqual(allowed, []string{"CarCreated", "MergeFailed"}) {
		t.Errorf("allowed = %v, want [CarCreated MergeFailed]", allowed)
	}
	if !reflect.DeepEqual(denied, []string{"CarMerged", "EngineStalled"}) {
		t.Errorf("denied = %v, want [CarMerged EngineStalled]", denied)
	}
}

func TestFilterAllowedEvents_EmptyAllowList(t *testing.T) {
	allow := newAllowList(config.AllowConfig{})
	allowed, denied := filterAllowedEvents([]string{"CarCreated", "MergeFailed"}, allow)
	if len(allowed) != 0 {
		t.Errorf("allowed = %v, want empty", allowed)
	}
	if !reflect.DeepEqual(denied, []string{"CarCreated", "MergeFailed"}) {
		t.Errorf("denied = %v, want both", denied)
	}
}

func TestFilterAllowedCommands_WildcardPrefix(t *testing.T) {
	allow := newAllowList(config.AllowConfig{Commands: []string{"foo.*", "ping"}})
	advertised := []string{"foo.bar", "foo.baz", "ping", "pong", "other.cmd"}
	allowed, denied := filterAllowedCommands(advertised, allow)
	wantAllowed := []string{"foo.bar", "foo.baz", "ping"}
	wantDenied := []string{"pong", "other.cmd"}
	if !reflect.DeepEqual(allowed, wantAllowed) {
		t.Errorf("allowed = %v, want %v", allowed, wantAllowed)
	}
	if !reflect.DeepEqual(denied, wantDenied) {
		t.Errorf("denied = %v, want %v", denied, wantDenied)
	}
}

func TestFilterAllowedCommands_Star(t *testing.T) {
	allow := newAllowList(config.AllowConfig{Commands: []string{"*"}})
	allowed, denied := filterAllowedCommands([]string{"a", "b.c"}, allow)
	if len(denied) != 0 {
		t.Errorf("denied = %v, want empty under \"*\"", denied)
	}
	if !reflect.DeepEqual(allowed, []string{"a", "b.c"}) {
		t.Errorf("allowed = %v, want all", allowed)
	}
}

// TestFilterAllowedEvents_SkipsEmptyStrings verifies the filter does
// not consider blank topic entries.
func TestFilterAllowedEvents_SkipsEmptyStrings(t *testing.T) {
	allow := newAllowList(config.AllowConfig{Events: []string{"*"}})
	allowed, denied := filterAllowedEvents([]string{"", "x", ""}, allow)
	if !reflect.DeepEqual(allowed, []string{"x"}) {
		t.Errorf("allowed = %v, want [x]", allowed)
	}
	if len(denied) != 0 {
		t.Errorf("denied = %v, want empty", denied)
	}
}
