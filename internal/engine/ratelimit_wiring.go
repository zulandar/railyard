package engine

// ObserveOutput is the exported wrapper around the package-private observeOutput
// method. It exists so callers outside this file (notably the engine's
// monitorSession in pkg/cli) can feed subprocess output bytes into the
// rate-limit detector. The underlying scan is the same — this is purely a
// visibility shim. Safe for concurrent invocation.
func (d *RateLimitDetector) ObserveOutput(p []byte) {
	d.observeOutput(p)
}

// AttachToSession wires the rate-limit detector into the session's stdout
// and stderr writers, chaining on top of any existing onWrite callbacks
// (typically set by NewStallDetector). The previous callback is preserved
// and invoked first, then the rate-limit detector observes the same bytes.
//
// Call AttachToSession after NewStallDetector so the stall detector's
// callback is the wrapped one — both detectors will then observe every
// byte that the logWriter writes.
func (d *RateLimitDetector) AttachToSession(sess *Session) {
	if sess == nil {
		return
	}
	if sess.stdout != nil {
		prev := sess.stdout.onWrite
		sess.stdout.onWrite = func(p []byte) {
			if prev != nil {
				prev(p)
			}
			d.observeOutput(p)
		}
	}
	if sess.stderr != nil {
		prev := sess.stderr.onWrite
		sess.stderr.onWrite = func(p []byte) {
			if prev != nil {
				prev(p)
			}
			d.observeOutput(p)
		}
	}
}
