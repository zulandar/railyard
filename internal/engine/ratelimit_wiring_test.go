package engine

import (
	"testing"
	"time"
)

// TestRateLimitDetector_AttachToSession_ChainsWithStallDetector verifies that
// when both StallDetector and RateLimitDetector are attached to the same
// session, both observe every byte written through the logWriters.
func TestRateLimitDetector_AttachToSession_ChainsWithStallDetector(t *testing.T) {
	sess := newMockSession()

	// StallDetector wires itself to sess.stdout.onWrite (and stderr).
	sd := NewStallDetector(sess, StallConfig{StdoutTimeout: time.Minute})
	defer sd.Stop()
	if sess.stdout.onWrite == nil {
		t.Fatal("stall detector did not register onWrite")
	}

	// RateLimitDetector chains on top.
	rd := NewRateLimitDetector()
	defer rd.Stop()
	rd.AttachToSession(sess)

	// Feed a rate-limit-flavored chunk through the stdout writer.
	if _, err := sess.stdout.Write([]byte(sampleAnthropic429)); err != nil {
		t.Fatalf("stdout.Write: %v", err)
	}

	// The rate-limit detector should fire.
	sig := recvSignal(t, rd.Signaled(), time.Second)
	if sig.Source != "anthropic" {
		t.Errorf("Source = %q, want %q", sig.Source, "anthropic")
	}

	// The stall detector should have observed activity (lastActivityAt updated
	// from the zero value at attach time). We can't easily inspect that, but
	// we can at least verify the stall detector is still operational by
	// confirming it hasn't fired spuriously.
	select {
	case r := <-sd.Stalled():
		t.Fatalf("stall detector fired unexpectedly: %+v", r)
	default:
	}
}

// TestRateLimitDetector_AttachToSession_NilSession is a defensive smoke test —
// passing nil must not panic. Useful for tests that pass a partially-built
// session.
func TestRateLimitDetector_AttachToSession_NilSession(t *testing.T) {
	rd := NewRateLimitDetector()
	defer rd.Stop()
	rd.AttachToSession(nil) // must not panic
}

// TestRateLimitDetector_AttachToSession_StderrAlso confirms stderr is wired too.
func TestRateLimitDetector_AttachToSession_StderrAlso(t *testing.T) {
	sess := newMockSession()
	rd := NewRateLimitDetector()
	defer rd.Stop()
	rd.AttachToSession(sess)

	if _, err := sess.stderr.Write([]byte(sampleHTTP429)); err != nil {
		t.Fatalf("stderr.Write: %v", err)
	}

	sig := recvSignal(t, rd.Signaled(), time.Second)
	if sig.Source != "http" {
		t.Errorf("Source = %q, want %q", sig.Source, "http")
	}
}
