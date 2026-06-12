// Optional sha256 pinning of plugin binaries at launch (railyard-77h.15).
//
// When an operator sets `plugins.<name>.sha256` in railyard.yaml, the host
// recomputes the SHA-256 of the resolved binary on EVERY launch — first
// boot AND every supervisor relaunch after a crash — and refuses to exec a
// binary whose hash does not match the pin. A mismatch permanently disables
// the plugin for the rest of the process lifetime with the distinct status
// reason "integrity-mismatch". Absent pin = no check (the default).
//
// TOCTOU note: this is integrity-against-drift, NOT a sandbox. We hash the
// file from a single open fd (computeFileSHA256) to shrink the window, but
// go-plugin ultimately re-opens and execs the binary BY PATH, so a
// sufficiently-privileged attacker who can rewrite the file between our hash
// and go-plugin's exec can still defeat the check. Closing that race fully
// would require handing go-plugin our fd (it does not support that) or an
// O_PATH/fexecve dance outside go-plugin's control — out of scope. The
// control's value is detecting an operator-visible binary swap across
// restarts, not defending against a root-equivalent live attacker. See
// docs/plugins/operating.md "Security notes".
package pluginhost

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// integrityMismatchReason is the distinct disabled-plugin status reason
// surfaced by [Host.Status] (the Error column) when a launch is refused
// because the binary's SHA-256 does not match the configured pin. Stable
// string — operators and the troubleshooting table key off it.
const integrityMismatchReason = "integrity-mismatch"

// integrityMismatchError is returned by [Host.launchPluginOnce] when the
// resolved binary's hash does not match the configured pin. Callers
// (initOne for first boot, supervise/relaunch for crash recovery) detect it
// via errors.As to route the failure to the permanent-disable path with the
// integrity-mismatch reason instead of the ordinary crash-budget loop.
type integrityMismatchError struct {
	expected string
	actual   string
}

func (e *integrityMismatchError) Error() string {
	return fmt.Sprintf("%s: binary sha256 %s does not match configured pin %s",
		integrityMismatchReason, e.actual, e.expected)
}

// computeFileSHA256 returns the lowercase hex SHA-256 of the file at path,
// hashed from a SINGLE open fd. Opening once (rather than stat-then-open or
// re-opening per read) is the TOCTOU mitigation described in the package
// doc: it removes the window between an os.Stat and the read. It does NOT
// close the residual race with go-plugin's own by-path exec.
func computeFileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // intentional: hashing the configured plugin binary
	if err != nil {
		return "", fmt.Errorf("open for hashing: %w", err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("read for hashing: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// resolvePinnedSha256 returns the configured SHA-256 pin for the named
// plugin, or "" when none is set (the default: no check). Config load has
// already validated the value is exactly 64 lowercase hex chars, so callers
// compare against computeFileSHA256's lowercase-hex output directly.
func (h *Host) resolvePinnedSha256(name string) string {
	if h.deps.Cfg == nil {
		return ""
	}
	s, ok := h.deps.Cfg.Plugins.Settings[name]
	if !ok {
		return ""
	}
	return s.Sha256
}

// verifyBinaryPin enforces the optional sha256 pin for candidate c before
// exec. It is invoked from [Host.launchPluginOnce] so BOTH the first-boot
// path and every supervisor relaunch re-verify (the spec's CRITICAL
// requirement). When no pin is configured it is a no-op. On mismatch it
// WARN-logs the expected vs actual hash and returns an
// *integrityMismatchError; a hashing failure (e.g. the binary vanished
// between discovery and launch) is returned verbatim.
func (h *Host) verifyBinaryPin(c candidate, logger *slog.Logger) error {
	pin := h.resolvePinnedSha256(c.name)
	if pin == "" {
		// No pin configured — default behavior, no check.
		return nil
	}
	actual, err := computeFileSHA256(c.path)
	if err != nil {
		return fmt.Errorf("integrity: hash plugin binary %s: %w", c.path, err)
	}
	if actual != pin {
		logger.Warn(
			"pluginhost: plugin binary integrity check FAILED — refusing to launch",
			slog.String("plugin", c.name),
			slog.String("path", c.path),
			slog.String("reason", integrityMismatchReason),
			slog.String("expected_sha256", pin),
			slog.String("actual_sha256", actual),
		)
		return &integrityMismatchError{expected: pin, actual: actual}
	}
	logger.Debug(
		"pluginhost: plugin binary integrity check passed",
		slog.String("plugin", c.name),
		slog.String("sha256", actual),
	)
	return nil
}
