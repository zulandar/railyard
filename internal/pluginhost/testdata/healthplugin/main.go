// Package main is a test subprocess plugin that DOES implement the
// optional pkg/plugin.HealthReporter interface (railyard-77h.12). It is
// the OK-path fixture for the host-side health-poll e2e: the host polls
// PluginService.Health, the adapter routes to this impl, and the verdict
// flows through to Host.Status().
//
// The reported state/message are controlled by env so a single binary can
// cover ok / degraded / failing without a rebuild:
//
//   - RAILYARD_HEALTHPLUGIN_STATE: "ok" (default) | "degraded" | "failing"
//   - RAILYARD_HEALTHPLUGIN_MSG:   message string (default "synthetic ok")
//
// It lives under testdata/ so `go build ./...` skips it; the host test
// shells out to the go toolchain to compile it on demand.
package main

import (
	"context"
	"os"

	"github.com/zulandar/railyard/pkg/plugin"
)

type healthPlugin struct{}

func (p *healthPlugin) Name() string                            { return "healthplugin" }
func (p *healthPlugin) Init(context.Context, plugin.Host) error { return nil }
func (p *healthPlugin) Start(context.Context) error             { return nil }
func (p *healthPlugin) Stop(context.Context) error              { return nil }

// Health implements plugin.HealthReporter. The verdict and message are
// read from env so the host-side test can assert each state maps through.
func (p *healthPlugin) Health(context.Context) (plugin.HealthStatus, string) {
	msg := os.Getenv("RAILYARD_HEALTHPLUGIN_MSG")
	if msg == "" {
		msg = "synthetic ok"
	}
	switch os.Getenv("RAILYARD_HEALTHPLUGIN_STATE") {
	case "degraded":
		return plugin.HealthDegraded, msg
	case "failing":
		return plugin.HealthFailing, msg
	default:
		return plugin.HealthOK, msg
	}
}

func main() {
	plugin.Serve(&healthPlugin{})
}
