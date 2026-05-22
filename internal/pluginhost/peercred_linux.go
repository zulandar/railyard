//go:build linux

package pluginhost

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// readPeerCred reads SO_PEERCRED on the supplied UDS connection and
// returns the peer's pid and uid. Returns an error on non-UDS conns or
// when the syscall fails.
func readPeerCred(conn net.Conn) (pid int32, uid uint32, err error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, 0, fmt.Errorf("pluginhost: peer-cred check requires a *net.UnixConn, got %T", conn)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, 0, fmt.Errorf("pluginhost: SyscallConn: %w", err)
	}

	var (
		cred    *unix.Ucred
		credErr error
	)
	cerr := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if cerr != nil {
		return 0, 0, fmt.Errorf("pluginhost: Control: %w", cerr)
	}
	if credErr != nil {
		return 0, 0, fmt.Errorf("pluginhost: getsockopt SO_PEERCRED: %w", credErr)
	}
	if cred == nil {
		return 0, 0, fmt.Errorf("pluginhost: nil Ucred from SO_PEERCRED")
	}
	return cred.Pid, cred.Uid, nil
}

// peerCredSupported reports whether the current platform implements
// SO_PEERCRED. Linux: true.
func peerCredSupported() bool { return true }
