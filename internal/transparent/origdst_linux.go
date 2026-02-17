//go:build linux

package transparent

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

// getOriginalDst recovers the original destination address before iptables
// REDIRECT changed it. Uses the SO_ORIGINAL_DST socket option (IPv4).
func getOriginalDst(conn net.Conn) (net.Addr, error) {
	tc, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, fmt.Errorf("origdst: not a TCP connection")
	}

	raw, err := tc.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("origdst: syscall conn: %w", err)
	}

	var origAddr syscall.RawSockaddrInet4
	var sysErr error

	err = raw.Control(func(fd uintptr) {
		const soOriginalDst = 80 // SO_ORIGINAL_DST
		size := uint32(unsafe.Sizeof(origAddr))
		_, _, errno := syscall.Syscall6(
			syscall.SYS_GETSOCKOPT,
			fd,
			syscall.SOL_IP,
			soOriginalDst,
			uintptr(unsafe.Pointer(&origAddr)), //nolint:gosec // required for syscall
			uintptr(unsafe.Pointer(&size)),     //nolint:gosec // required for syscall
			0,
		)
		if errno != 0 {
			sysErr = fmt.Errorf("origdst: getsockopt SO_ORIGINAL_DST: %w", errno)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("origdst: control: %w", err)
	}
	if sysErr != nil {
		return nil, sysErr
	}

	// Port is stored in network byte order (big-endian) as a uint16.
	// Swap bytes to get host order on little-endian systems.
	port := int(origAddr.Port>>8 | origAddr.Port<<8)
	ip := net.IPv4(origAddr.Addr[0], origAddr.Addr[1], origAddr.Addr[2], origAddr.Addr[3])

	return &net.TCPAddr{IP: ip, Port: port}, nil
}
