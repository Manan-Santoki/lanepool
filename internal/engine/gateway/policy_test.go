package gateway

import (
	"net/netip"
	"testing"

	"github.com/Manan-Santoki/lanepool/internal/auth"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

// The proxy listens before the first config arrives. Connections in that
// window must not count towards the per-IP lockout, or a torrent client that
// opens dozens of connections is locked out for five minutes after every
// engine restart.
func TestNoLockoutBeforeFirstConfig(t *testing.T) {
	p := NewPolicy()
	client := netip.MustParseAddr("10.200.19.20")
	for i := 0; i < 50; i++ {
		if _, _, res := p.Authenticate("qbit", "password123", client); res != protocol.ResultPaused {
			t.Fatalf("before config: result %q, want %q", res, protocol.ResultPaused)
		}
	}
	hash, _ := auth.HashPassword("password123")
	p.Apply(protocol.DefaultSettings(), []protocol.UserSpec{{ID: 1, Username: "qbit", PasswordHash: hash, Enabled: true}}, nil)
	if u, _, res := p.Authenticate("qbit", "password123", client); res != "" || u == nil {
		t.Fatalf("after config: result %q", res)
	}

	// Real failures still lock the IP out.
	for i := 0; i < 10; i++ {
		p.Authenticate("qbit", "wrong-password", client)
	}
	if _, _, res := p.Authenticate("qbit", "password123", client); res != protocol.ResultAuthFailed {
		t.Fatalf("after 10 failures: result %q, want lockout", res)
	}
}
