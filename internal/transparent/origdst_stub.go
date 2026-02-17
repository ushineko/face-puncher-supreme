//go:build !linux

package transparent

import (
	"fmt"
	"net"
	"runtime"
)

// getOriginalDst is not supported on this platform.
func getOriginalDst(_ net.Conn) (net.Addr, error) {
	return nil, fmt.Errorf("origdst: SO_ORIGINAL_DST not supported on %s", runtime.GOOS)
}
