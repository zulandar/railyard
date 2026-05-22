//go:build !linux

package pluginhost

import (
	"errors"
	"net"
)

// errPeerCredUnsupported is returned by readPeerCred on platforms that
// do not implement SO_PEERCRED. The caller is expected to consult
// peerCredSupported() and skip the check there.
var errPeerCredUnsupported = errors.New("pluginhost: SO_PEERCRED not supported on this platform")

// readPeerCred is unsupported on non-Linux platforms.
func readPeerCred(_ net.Conn) (int32, uint32, error) {
	return 0, 0, errPeerCredUnsupported
}

// peerCredSupported reports whether the current platform implements
// SO_PEERCRED. Non-Linux: false.
func peerCredSupported() bool { return false }
