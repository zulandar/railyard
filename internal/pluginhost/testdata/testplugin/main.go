// Package main is the test subprocess plugin built and launched by
// internal/pluginhost end-to-end tests.
//
// It is a minimal plugin.Plugin that subscribes to CarCreated when its
// Start is invoked and records every event it sees in a temp file (path
// supplied by env RAILYARD_TESTPLUGIN_LOG). The host-side tests
// publish into the bus and assert the file picks up the expected lines.
//
// The file lives under testdata/ so `go build ./...` skips it; the test
// shells out to the go toolchain to compile it on demand.
package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/zulandar/railyard/pkg/plugin"
)

type testPlugin struct {
	mu    sync.Mutex
	unsub plugin.Unsubscribe
	out   *os.File
}

func (p *testPlugin) Name() string { return "testplugin" }

func (p *testPlugin) Init(_ context.Context, h plugin.Host) error {
	logPath := os.Getenv("RAILYARD_TESTPLUGIN_LOG")
	if logPath == "" {
		return nil
	}
	f, err := os.Create(logPath)
	if err != nil {
		return err
	}
	p.out = f
	fmt.Fprintln(p.out, "init ok")

	// Pull YardInfo through the host as a smoke check.
	yi := h.YardInfo()
	fmt.Fprintf(p.out, "yard project=%s owner=%s\n", yi.Project, yi.Owner)

	p.unsub = h.Subscribe(plugin.CarCreated, func(_ plugin.EventType, payload any) {
		ev, ok := payload.(plugin.CarCreatedEvent)
		if !ok {
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		fmt.Fprintf(p.out, "event car_id=%s\n", ev.CarID)
		_ = p.out.Sync()
	})
	return nil
}

func (p *testPlugin) Start(_ context.Context) error {
	if p.out != nil {
		fmt.Fprintln(p.out, "start ok")
		_ = p.out.Sync()
	}
	return nil
}

func (p *testPlugin) Stop(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unsub != nil {
		p.unsub()
	}
	if p.out != nil {
		fmt.Fprintln(p.out, "stop ok")
		_ = p.out.Sync()
		_ = p.out.Close()
	}
	return nil
}

func main() {
	plugin.Serve(&testPlugin{})
}
