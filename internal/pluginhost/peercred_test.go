package pluginhost

import (
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// TestVerifyPeerCredLocalConnection sanity-checks the SO_PEERCRED path
// against an in-test UDS where both peers are this process.
//
// On Linux the peer pid will equal os.Getpid() and the peer uid will
// equal os.Getuid() — the verification accepts the conn when we pass
// expectedPid=os.Getpid() and rejects it when we pass a clearly wrong
// pid (the host process can't forge its own pid downward).
func TestVerifyPeerCredLocalConnection(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SO_PEERCRED is Linux-only")
	}

	dir := t.TempDir()
	sock := filepath.Join(dir, "probe.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	// Accept goroutine — just closes incoming conns.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		wg.Wait()
	})

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Happy path — expectedPid matches ourselves.
	if err := verifyPeerCredFor(sock, os.Getpid(), logger); err != nil {
		t.Errorf("expected verify to succeed for self-connection, got %v", err)
	}

	// Negative path — clearly bogus pid.
	if err := verifyPeerCredFor(sock, 0xDEADBEEF, logger); err == nil {
		t.Error("expected verify to fail on pid mismatch")
	}
}
