package pluginhost

import (
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

func TestAllowList_Empty_DeniesAll(t *testing.T) {
	a := newAllowList(config.AllowConfig{})
	if a.AllowEvent("CarCreated") {
		t.Error("empty allow-list should deny CarCreated")
	}
	if a.AllowCommand("foo.bar") {
		t.Error("empty allow-list should deny foo.bar")
	}
	if !a.IsEmpty() {
		t.Error("IsEmpty should be true for zero AllowList")
	}
}

func TestAllowList_EventLiteralMatch(t *testing.T) {
	a := newAllowList(config.AllowConfig{
		Events: []string{"CarCreated", "CarMerged"},
	})
	cases := []struct {
		topic string
		want  bool
	}{
		{"CarCreated", true},
		{"CarMerged", true},
		{"MergeFailed", false},
		{"", false},
		{"CarCreatedXtra", false},
	}
	for _, tc := range cases {
		if got := a.AllowEvent(tc.topic); got != tc.want {
			t.Errorf("AllowEvent(%q) = %v, want %v", tc.topic, got, tc.want)
		}
	}
}

func TestAllowList_EventStarWildcard(t *testing.T) {
	a := newAllowList(config.AllowConfig{Events: []string{"*"}})
	for _, topic := range []string{"CarCreated", "MergeFailed", "anything", "x.y.z"} {
		if !a.AllowEvent(topic) {
			t.Errorf("\"*\" should allow %q", topic)
		}
	}
}

func TestAllowList_CommandLiteralMatch(t *testing.T) {
	a := newAllowList(config.AllowConfig{Commands: []string{"foo", "bar.baz"}})
	cases := []struct {
		name string
		want bool
	}{
		{"foo", true},
		{"bar.baz", true},
		{"bar", false},
		{"foo.bar", false},
		{"baz", false},
	}
	for _, tc := range cases {
		if got := a.AllowCommand(tc.name); got != tc.want {
			t.Errorf("AllowCommand(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAllowList_CommandStarWildcard(t *testing.T) {
	a := newAllowList(config.AllowConfig{Commands: []string{"*"}})
	for _, name := range []string{"foo", "ns.cmd", "x.y.z", "anything"} {
		if !a.AllowCommand(name) {
			t.Errorf("\"*\" should allow %q", name)
		}
	}
}

func TestAllowList_CommandPrefixWildcard(t *testing.T) {
	a := newAllowList(config.AllowConfig{Commands: []string{"dispatch.*"}})
	cases := []struct {
		name string
		want bool
	}{
		{"dispatch.foo", true},
		{"dispatch.bar.baz", true},
		// Must include the dot — "dispatchfoo" should NOT match "dispatch.*".
		{"dispatchfoo", false},
		{"dispatch", false},
		{"car.merged", false},
	}
	for _, tc := range cases {
		if got := a.AllowCommand(tc.name); got != tc.want {
			t.Errorf("AllowCommand(%q) under [dispatch.*] = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAllowList_CommandMultipleEntries(t *testing.T) {
	a := newAllowList(config.AllowConfig{Commands: []string{"ping", "dispatch.*"}})
	for _, want := range []string{"ping", "dispatch.start", "dispatch.stop"} {
		if !a.AllowCommand(want) {
			t.Errorf("AllowCommand(%q) = false, want true", want)
		}
	}
	for _, deny := range []string{"pong", "dispatchfoo", "other.cmd"} {
		if a.AllowCommand(deny) {
			t.Errorf("AllowCommand(%q) = true, want false", deny)
		}
	}
}

func TestAllowList_IsEmpty(t *testing.T) {
	if !(AllowList{}).IsEmpty() {
		t.Error("zero AllowList should be IsEmpty")
	}
	if (AllowList{events: []string{"CarCreated"}}).IsEmpty() {
		t.Error("AllowList with events should not be IsEmpty")
	}
	if (AllowList{commands: []string{"foo"}}).IsEmpty() {
		t.Error("AllowList with commands should not be IsEmpty")
	}
}

func TestAllowList_EventsCommandsCopy(t *testing.T) {
	// Mutating the input slice after construction must not affect the
	// AllowList — the constructor copies.
	in := config.AllowConfig{
		Events:   []string{"CarCreated"},
		Commands: []string{"foo"},
	}
	a := newAllowList(in)
	in.Events[0] = "Mutated"
	in.Commands[0] = "Mutated"
	if !a.AllowEvent("CarCreated") {
		t.Error("AllowList captured input slice by reference")
	}
	if !a.AllowCommand("foo") {
		t.Error("AllowList captured input slice by reference")
	}

	// And the accessors return copies too.
	out := a.Events()
	out[0] = "Mutated"
	if !a.AllowEvent("CarCreated") {
		t.Error("Events() returned the underlying slice")
	}
	outc := a.Commands()
	outc[0] = "Mutated"
	if !a.AllowCommand("foo") {
		t.Error("Commands() returned the underlying slice")
	}
}
