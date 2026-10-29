package gateway

import (
	"io"
	"net"
	"testing"
	"time"
)

type stubTCPConn struct {
	local net.Addr
}

func (s stubTCPConn) LocalAddr() net.Addr              { return s.local }
func (s stubTCPConn) RemoteAddr() net.Addr             { return nil }
func (s stubTCPConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (s stubTCPConn) Write([]byte) (int, error)        { return 0, io.EOF }
func (s stubTCPConn) Close() error                     { return nil }
func (s stubTCPConn) SetDeadline(time.Time) error      { return nil }
func (s stubTCPConn) SetReadDeadline(time.Time) error  { return nil }
func (s stubTCPConn) SetWriteDeadline(time.Time) error { return nil }

// Multi-homed engine (e.g. dokploy-network + qbit-egress): auto must advertise the
// local address of the TCP control socket — the IP the client dialed.
func TestUDPAdvertiseHostAutoPerClientNetwork(t *testing.T) {
	s := &Server{UDP: UDPConfig{AdvertiseHost: "auto"}}
	for _, tc := range []struct {
		local, want string
	}{
		{"10.200.19.10", "10.200.19.10"},
		{"10.0.1.42", "10.0.1.42"},
	} {
		ip := net.ParseIP(tc.local)
		got := s.advertiseHostForClient(stubTCPConn{local: &net.TCPAddr{IP: ip, Port: 8080}})
		if got != tc.want {
			t.Errorf("local %s: got %q want %q", tc.local, got, tc.want)
		}
	}
}

func TestUDPRelaySocketBindsAllInterfaces(t *testing.T) {
	pp := newPortPool(39600, 39601)
	c, _, err := pp.acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer pp.release(c)
	ua, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("local addr type %T", c.LocalAddr())
	}
	if !ua.IP.IsUnspecified() {
		t.Fatalf("relay bind IP %v, want unspecified (all interfaces)", ua.IP)
	}
}
