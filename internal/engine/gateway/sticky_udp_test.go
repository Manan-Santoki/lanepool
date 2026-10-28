package gateway

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/auth"
	"github.com/Manan-Santoki/lanepool/internal/engine/lanes"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

// hideLaneLanes drops one lane from Usable() to simulate it going down.
type hideLaneLanes struct {
	inner Lanes
	hide  string
}

func (h *hideLaneLanes) Usable() []lanes.Info {
	var out []lanes.Info
	for _, l := range h.inner.Usable() {
		if l.ID != h.hide {
			out = append(out, l)
		}
	}
	return out
}

func (h *hideLaneLanes) Dial(ctx context.Context, id, address string) (net.Conn, func(), error) {
	return h.inner.Dial(ctx, id, address)
}

func (h *hideLaneLanes) ListenPacket(ctx context.Context, id string) (net.PacketConn, func(), error) {
	return h.inner.ListenPacket(ctx, id)
}

func (h *hideLaneLanes) LookupHost(ctx context.Context, id, host string) (netip.Addr, error) {
	return h.inner.LookupHost(ctx, id, host)
}

// Sticky TCP and UDP must share a lane even when per-host burned IPs would
// shrink the candidate set for CONNECT only (UDP ASSOCIATE has no target host).
func TestStickySessionTCPAndUDPSameLane(t *testing.T) {
	e := newEnv(t, "us", "us")
	hash, _ := auth.HashPassword("password123")
	e.addUser(protocol.UserSpec{
		Username: "app", Enabled: true, PasswordHash: hash,
		UDPEnabled: true, LogMode: protocol.LogModeOff,
	}, "password123")
	e.burned = []protocol.BurnedIP{{
		Domain: "ipify.org", LaneID: "test:us-1", ExpiresAt: time.Now().Add(time.Hour),
	}}
	e.apply()
	e.srv.UDP = UDPConfig{AdvertiseHost: "127.0.0.1", PortMin: 39100, PortMax: 39110}

	const user = "app-country-us-session-sticky1-sessttl-60"

	_, _, ctrl, err := socks5Associate(e.srv.Addr(), user, "password123")
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()

	udpLane := laneForProtocol(e, "socks5-udp")
	if udpLane == "" {
		t.Fatal("no UDP association in live list")
	}

	ip, err := e.socksGet(user, "password123")
	if err != nil {
		t.Fatal(err)
	}
	tcpLane := laneIDForExitIP(e, ip)
	if tcpLane == "" {
		t.Fatal("no lane for exit IP", ip)
	}
	if udpLane != tcpLane {
		t.Fatalf("UDP on %s, TCP on %s (exit %s); want the same sticky lane", udpLane, tcpLane, ip)
	}
	ctrl.Close()
}

// When a sticky lane drops out of the pool, long-lived UDP ASSOCIATE must move
// with the session to the replacement lane, like new TCP connections do.
func TestStickyUDPFollowsLaneFailover(t *testing.T) {
	e := newEnv(t, "us", "us")
	hash, _ := auth.HashPassword("password123")
	e.addUser(protocol.UserSpec{
		Username: "app", Enabled: true, PasswordHash: hash,
		UDPEnabled: true, LogMode: protocol.LogModeOff,
	}, "password123")
	e.apply()
	e.srv.UDP = UDPConfig{AdvertiseHost: "127.0.0.1", PortMin: 39200, PortMax: 39210}

	const user = "app-session-fail1-sessttl-60"
	firstIP, err := e.socksGet(user, "password123")
	if err != nil {
		t.Fatal(err)
	}
	firstLane := laneIDForExitIP(e, firstIP)
	if firstLane == "" {
		t.Fatal("missing lane for first TCP")
	}

	_, _, ctrl, err := socks5Associate(e.srv.Addr(), user, "password123")
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	if lane := laneForProtocol(e, "socks5-udp"); lane != firstLane {
		t.Fatalf("UDP lane %s, want %s", lane, firstLane)
	}

	hide := &hideLaneLanes{inner: e.lanes, hide: firstLane}
	e.srv.Lanes = hide

	deadline := time.Now().Add(5 * time.Second)
	var udpLane, tcpLane string
	for time.Now().Before(deadline) {
		secondIP, err := e.socksGet(user, "password123")
		if err != nil {
			t.Fatal(err)
		}
		tcpLane = laneIDForExitIP(e, secondIP)
		udpLane = laneForProtocol(e, "socks5-udp")
		if tcpLane != "" && udpLane != "" && tcpLane == udpLane && tcpLane != firstLane {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if udpLane == "" || tcpLane == "" {
		t.Fatalf("lanes not observed: udp=%s tcp=%s", udpLane, tcpLane)
	}
	if udpLane != tcpLane {
		t.Fatalf("after failover UDP %s != TCP %s", udpLane, tcpLane)
	}
	if udpLane == firstLane {
		t.Fatalf("still on failed lane %s", firstLane)
	}
	ctrl.Close()
}

func laneForProtocol(e *env, proto string) string {
	for _, c := range e.srv.Live() {
		if c.Protocol == proto {
			return c.LaneID
		}
	}
	return ""
}

func laneIDForExitIP(e *env, exitIP string) string {
	for _, l := range e.lanes.Usable() {
		if l.ExitIP == exitIP {
			return l.ID
		}
	}
	return ""
}
