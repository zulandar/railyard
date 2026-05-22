// Package main is the test fixture used by the restart-loop integration
// tests in railyard-fll.6. It always launches successfully (so the host's
// initial Init + Start path completes) and then crashes the subprocess
// on its NEXT lifecycle hook, configurable via the env vars below.
//
// Crash modes:
//   - RAILYARD_CRASHPLUGIN_MODE=after_start    — Start succeeds, the
//     subprocess then calls os.Exit(1) shortly after returning from
//     Start. This is the "crash AFTER Init/Start handshake" path the
//     supervisor restart loop exercises.
//   - RAILYARD_CRASHPLUGIN_MODE=on_start       — Start synchronously
//     calls os.Exit(1) before returning. The host observes the
//     subprocess vanish during the Start RPC (this is harder to
//     coordinate but mirrors a plugin that immediately segfaults).
//
// Optional counter:
//   - RAILYARD_CRASHPLUGIN_COUNTER_FILE — if set, the plugin appends
//     a line "pid=<pid>\n" each time it boots. The test reads this
//     file to count how many times the host has relaunched it.
//
// The fixture lives under testdata/ so `go build ./...` skips it; the
// test shells out to the go toolchain to compile it on demand.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/zulandar/railyard/pkg/plugin"
)

type crashPlugin struct{}

func (p *crashPlugin) Name() string { return "crashplugin" }

func (p *crashPlugin) Init(_ context.Context, _ plugin.Host) error {
	// Append the per-boot marker so the test can count relaunches.
	if path := os.Getenv("RAILYARD_CRASHPLUGIN_COUNTER_FILE"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "pid=%d\n", os.Getpid())
			_ = f.Close()
		}
	}
	return nil
}

func (p *crashPlugin) Start(_ context.Context) error {
	mode := os.Getenv("RAILYARD_CRASHPLUGIN_MODE")
	switch mode {
	case "on_start":
		// Crash inside the Start RPC, before returning. os.Exit
		// does not return; we add a return statement after the
		// call so the compiler is happy.
		os.Exit(1)
		return nil
	case "after_start":
		// Return cleanly from Start, then crash on a short delay so
		// the host's Start RPC completes successfully and the
		// supervisor observes a post-handshake crash.
		go func() {
			time.Sleep(50 * time.Millisecond)
			os.Exit(1)
		}()
		return nil
	default:
		// No crash configured — behave like a normal plugin.
		return nil
	}
}

func (p *crashPlugin) Stop(_ context.Context) error {
	return nil
}

func main() {
	plugin.Serve(&crashPlugin{})
}
