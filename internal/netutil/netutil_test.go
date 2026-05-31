package netutil

import (
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestIsUnspecifiedDialError(t *testing.T) {
	opErr := func(ip net.IP) error {
		return &net.OpError{
			Op:   "dial",
			Net:  "tcp",
			Addr: &net.TCPAddr{IP: ip, Port: 443},
			Err:  errors.New("connect: connection refused"),
		}
	}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"ipv4 unspecified", opErr(net.IPv4zero), true},
		{"ipv6 unspecified", opErr(net.IPv6unspecified), true},
		{"wrapped unspecified", fmt.Errorf("tunnel: %w", opErr(net.IPv4zero)), true},
		{"routable ipv4", opErr(net.ParseIP("93.184.216.34")), false},
		{"loopback is not unspecified", opErr(net.IPv4(127, 0, 0, 1)), false},
		{"nil error", nil, false},
		{"non-operror", errors.New("some other failure"), false},
		{"operror without tcpaddr", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("boom")}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsUnspecifiedDialError(tt.err); got != tt.want {
				t.Errorf("IsUnspecifiedDialError() = %v, want %v", got, tt.want)
			}
		})
	}
}
