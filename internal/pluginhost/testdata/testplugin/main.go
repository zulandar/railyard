// Package main is the test subprocess plugin built and launched by
// internal/pluginhost end-to-end tests.
//
// Default mode: a minimal plugin.Plugin that subscribes to CarCreated
// when its Start is invoked and records every event it sees in a temp
// file (path supplied by env RAILYARD_TESTPLUGIN_LOG). The host-side
// tests publish into the bus and assert the file picks up the expected
// lines.
//
// Slow-burst mode (env RAILYARD_TESTPLUGIN_SLOW=1) is the fixture used
// by Lane L's railyard-fll.5.3 backpressure integration test
// (subscribe_e2e_test.go). In slow mode the plugin subscribes to FIVE
// topics — CarCreated, CarClaimed, CarStatusChanged, CarMerged,
// MergeFailed — and each handler:
//
//   - Sleeps RAILYARD_TESTPLUGIN_SLOW_MS milliseconds (default 2) to
//     create the backpressure the host-side bounded buffer needs to
//     observe drops.
//   - Appends "<topic> <CarID>\n" to a per-topic log file under
//     RAILYARD_TESTPLUGIN_LOGDIR. The CarID is expected to be the
//     stringified monotonically-increasing publish-sequence number so
//     the host-side test can assert per-topic delivery order is
//     non-decreasing despite drops.
//
// The default (non-slow) mode is unchanged to avoid breaking the
// existing TestLaunchPluginHappyPath fixture.
//
// The file lives under testdata/ so `go build ./...` skips it; the test
// shells out to the go toolchain to compile it on demand.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/zulandar/railyard/pkg/plugin"
)

type testPlugin struct {
	mu    sync.Mutex
	unsub plugin.Unsubscribe
	out   *os.File

	// Slow-mode state (Lane L). One unsubscribe + one log file per
	// subscribed topic.
	slowMu      sync.Mutex
	slowUnsubs  []plugin.Unsubscribe
	slowOuts    map[string]*os.File
	slowSleep   time.Duration
	slowLogDir  string
	slowEnabled bool

	// Meta-mode state (railyard-77h.10). Subscribes to CarCreated via
	// SubscribeWithMeta and logs "<seq> <dropped>" per delivery so the
	// host-side e2e can observe gap detection end-to-end.
	metaMu      sync.Mutex
	metaEnabled bool
	metaOut     *os.File
	metaSleep   time.Duration
	metaUnsub   plugin.Unsubscribe

	// Emit-mode state (railyard-77h.9). Subscribes to its own namespaced
	// topic and emits onto it from Start, logging each received dynamic
	// payload so the host-side e2e can prove the EmitEvent -> bus ->
	// Subscribe -> SDK-decode round-trip across the process boundary.
	emitMu       sync.Mutex
	emitEnabled  bool
	emitHost     plugin.Host
	emitOut      *os.File
	emitUnsub    plugin.Unsubscribe
	emitStopCh   chan struct{}
	emitStopOnce sync.Once
}

const emitTopic = "testplugin.ping"

func (p *testPlugin) Name() string { return "testplugin" }

func (p *testPlugin) Init(_ context.Context, h plugin.Host) error {
	// Emit mode (railyard-77h.9). Subscribe to our own namespaced topic
	// and record each received dynamic payload; Start drives the emit.
	if os.Getenv("RAILYARD_TESTPLUGIN_EMIT") == "1" {
		p.emitEnabled = true
		p.emitHost = h
		p.emitStopCh = make(chan struct{})
		path := os.Getenv("RAILYARD_TESTPLUGIN_EMIT_LOG")
		if path == "" {
			return fmt.Errorf("testplugin: emit mode requires RAILYARD_TESTPLUGIN_EMIT_LOG")
		}
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("testplugin: create emit log: %w", err)
		}
		p.emitOut = f
		p.emitUnsub = h.Subscribe(plugin.EventType(emitTopic), func(topic plugin.EventType, payload any) {
			m, ok := payload.(map[string]any)
			if !ok {
				return
			}
			msg, _ := m["msg"].(string)
			p.emitMu.Lock()
			fmt.Fprintf(p.emitOut, "%s %v\n", topic, msg)
			_ = p.emitOut.Sync()
			p.emitMu.Unlock()
		})
		return nil
	}

	// Meta mode (railyard-77h.10). Subscribe to CarCreated with stream
	// metadata and record "<seq> <dropped>" per delivery, sleeping to
	// induce backpressure so the host-side e2e can observe a gap.
	if os.Getenv("RAILYARD_TESTPLUGIN_META") == "1" {
		p.metaEnabled = true
		ms := 2
		if raw := os.Getenv("RAILYARD_TESTPLUGIN_META_SLEEP_MS"); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
				ms = v
			}
		}
		p.metaSleep = time.Duration(ms) * time.Millisecond
		path := os.Getenv("RAILYARD_TESTPLUGIN_META_LOG")
		if path == "" {
			return fmt.Errorf("testplugin: meta mode requires RAILYARD_TESTPLUGIN_META_LOG")
		}
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("testplugin: create meta log: %w", err)
		}
		p.metaOut = f
		p.metaUnsub = h.SubscribeWithMeta(plugin.CarCreated, func(_ plugin.EventType, _ any, meta plugin.EventMeta) {
			p.metaMu.Lock()
			fmt.Fprintf(p.metaOut, "%d %d\n", meta.Seq, meta.Dropped)
			_ = p.metaOut.Sync()
			p.metaMu.Unlock()
			if p.metaSleep > 0 {
				time.Sleep(p.metaSleep)
			}
		})
		return nil
	}

	// Slow-burst mode (railyard-fll.5.3). Detected up front so default
	// mode below stays a no-op for slow-only env state.
	if os.Getenv("RAILYARD_TESTPLUGIN_SLOW") == "1" {
		p.slowEnabled = true
		ms := 2
		if raw := os.Getenv("RAILYARD_TESTPLUGIN_SLOW_MS"); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
				ms = v
			}
		}
		p.slowSleep = time.Duration(ms) * time.Millisecond
		p.slowLogDir = os.Getenv("RAILYARD_TESTPLUGIN_LOGDIR")
		p.slowOuts = make(map[string]*os.File)
		if p.slowLogDir == "" {
			return fmt.Errorf("testplugin: slow mode requires RAILYARD_TESTPLUGIN_LOGDIR")
		}
		if err := os.MkdirAll(p.slowLogDir, 0o755); err != nil {
			return fmt.Errorf("testplugin: mkdir log dir: %w", err)
		}
		// Open one file per topic and wire 5 subscriptions.
		for _, et := range slowTopics {
			path := filepath.Join(p.slowLogDir, string(et)+".log")
			f, err := os.Create(path)
			if err != nil {
				return fmt.Errorf("testplugin: create %s: %w", path, err)
			}
			p.slowOuts[string(et)] = f
			topic := et // capture
			unsub := h.Subscribe(topic, func(_ plugin.EventType, payload any) {
				p.slowHandle(topic, payload)
			})
			p.slowUnsubs = append(p.slowUnsubs, unsub)
		}
		return nil
	}

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

// slowTopics is the closed set of topics the slow-burst integration
// test subscribes to. Five entries — matches railyard-fll.5.3
// acceptance criterion (5).
var slowTopics = []plugin.EventType{
	plugin.CarCreated,
	plugin.CarClaimed,
	plugin.CarStatusChanged,
	plugin.CarMerged,
	plugin.MergeFailed,
}

// slowHandle is the slow handler used in burst mode. It records the
// event's CarID (an integer-as-string per the test contract) to the
// per-topic log, then sleeps to create backpressure. The sleep happens
// AFTER the write so the host-side reader sees deliveries even if the
// test cancels mid-burst.
func (p *testPlugin) slowHandle(topic plugin.EventType, payload any) {
	var carID string
	switch v := payload.(type) {
	case plugin.CarCreatedEvent:
		carID = v.CarID
	case plugin.CarClaimedEvent:
		carID = v.CarID
	case plugin.CarStatusChangedEvent:
		carID = v.CarID
	case plugin.CarMergedEvent:
		carID = v.CarID
	case plugin.MergeFailedEvent:
		carID = v.CarID
	default:
		return
	}
	p.slowMu.Lock()
	f := p.slowOuts[string(topic)]
	p.slowMu.Unlock()
	if f == nil {
		return
	}
	// One line per delivery: <carID>\n. Mutex-guarded so the (single)
	// drain goroutine for this topic does not race against Stop closing
	// the file.
	p.slowMu.Lock()
	_, _ = fmt.Fprintln(f, carID)
	_ = f.Sync()
	p.slowMu.Unlock()

	if p.slowSleep > 0 {
		time.Sleep(p.slowSleep)
	}
}

func (p *testPlugin) Start(_ context.Context) error {
	if p.out != nil {
		fmt.Fprintln(p.out, "start ok")
		_ = p.out.Sync()
	}
	// Emit mode: repeatedly publish onto our own topic until Stop. The
	// repetition tolerates the brief window before the Subscribe stream
	// is wired host-side (railyard-77h.9).
	if p.emitEnabled && p.emitHost != nil {
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-p.emitStopCh:
					return
				case <-ticker.C:
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					_ = p.emitHost.Emit(ctx, emitTopic, map[string]any{"msg": "pong"})
					cancel()
				}
			}
		}()
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
	// Emit-mode shutdown: stop the emit loop, unsubscribe, close the log.
	if p.emitEnabled {
		p.emitStopOnce.Do(func() { close(p.emitStopCh) })
		if p.emitUnsub != nil {
			p.emitUnsub()
		}
		p.emitMu.Lock()
		if p.emitOut != nil {
			_ = p.emitOut.Sync()
			_ = p.emitOut.Close()
			p.emitOut = nil
		}
		p.emitMu.Unlock()
	}

	// Meta-mode shutdown: unsubscribe then close the meta log under the
	// same mutex the handler uses for its writes.
	if p.metaEnabled {
		if p.metaUnsub != nil {
			p.metaUnsub()
		}
		p.metaMu.Lock()
		if p.metaOut != nil {
			_ = p.metaOut.Sync()
			_ = p.metaOut.Close()
			p.metaOut = nil
		}
		p.metaMu.Unlock()
	}

	// Slow-mode shutdown: unsubscribe each topic then close every
	// per-topic file. Mutex protects the close against an in-flight
	// drain goroutine (slowHandle takes the same mutex around its
	// write).
	if p.slowEnabled {
		for _, u := range p.slowUnsubs {
			if u != nil {
				u()
			}
		}
		p.slowMu.Lock()
		for _, f := range p.slowOuts {
			_ = f.Sync()
			_ = f.Close()
		}
		p.slowOuts = nil
		p.slowMu.Unlock()
	}
	return nil
}

func main() {
	plugin.Serve(&testPlugin{})
}
