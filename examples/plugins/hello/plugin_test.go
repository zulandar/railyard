// plugin_test.go demonstrates the recommended pattern for unit-testing
// a railyard plugin: drive Init/Start with a [plugintest.FakeHost], fire
// a synthetic event, and assert on the captured logs.
//
// The full SDK helper lives at pkg/plugin/plugintest in the railyard
// module. Anything more elaborate than this — multi-event flows, command
// dispatch round-trips, config-driven branches — is also a plugintest
// pattern; this file is the smallest case.
package main

import (
	"context"
	"testing"

	"github.com/zulandar/railyard/pkg/plugin"
	"github.com/zulandar/railyard/pkg/plugin/plugintest"
)

func TestHelloLogsCarCreated(t *testing.T) {
	fh := &plugintest.FakeHost{}
	p := &Hello{}

	if err := p.Init(context.Background(), fh); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	fh.DriveEvent(plugin.CarCreated, plugin.CarCreatedEvent{
		CarID: "c-1",
		Track: "go",
		Type:  "feature",
	})

	logs := fh.Logs()
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Message != "hello: car created" {
		t.Fatalf("unexpected log message: %q", logs[0].Message)
	}
}
