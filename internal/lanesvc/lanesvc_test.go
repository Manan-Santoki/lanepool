package lanesvc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/engine/lanes"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/wg/wgtest"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// lanesProcess is a lanes process with real (fake-provider) tunnels whose
// manager can be swapped out, like the process being redeployed.
type lanesProcess struct {
	t   *testing.T
	cur atomic.Pointer[Server]
	mgr atomic.Pointer[lanes.Manager]
	srv *httptest.Server
}

func newLanesProcess(t *testing.T) *lanesProcess {
	p := &lanesProcess{t: t}
	p.restart()
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.cur.Load().ServeHTTP(w, r) }))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *lanesProcess) restart() {
	m := lanes.New(protocol.DefaultSettings(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	p.t.Cleanup(cancel)
	go m.Run(ctx, 50*time.Millisecond)
	p.mgr.Store(m)
	time.Sleep(2 * time.Millisecond) // a distinct start time
	p.cur.Store(NewServer(m, "tok", quiet))
}

func specs(t *testing.T, exits ...string) []protocol.LaneSpec {
	var out []protocol.LaneSpec
	for i, ip := range exits {
		priv, pub := wgtest.KeyPair()
		prov := wgtest.Start(t, ip, pub)
		out = append(out, protocol.LaneSpec{
			ID: fmt.Sprintf("test:%d", i), Name: fmt.Sprint(i), CountryCode: "US", Enabled: true,
			Endpoint: prov.Endpoint, PeerKey: prov.PublicKey,
			Addresses: []string{wgtest.ClientAddr.String()}, DNS: []string{wgtest.DNSAddr.String()},
			Keys: []protocol.LaneKey{{ID: 1, PrivateKey: priv}},
		})
	}
	return out
}

func settings() protocol.EngineSettings {
	s := protocol.DefaultSettings()
	s.LaneStartDelay, s.MaxConnecting, s.ConnectTimeout = 0, 10, 5
	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// exitIP fetches http://api.ipify.org/ (the fake provider's web server) through
// lane id via the client.
func exitIP(t *testing.T, c *Client, id string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, release, err := c.Dial(ctx, id, wgtest.WebAddr.String()+":80")
	if err != nil {
		return "", err
	}
	defer release()
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(60 * time.Second))
	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: api.ipify.org\r\nConnection: close\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(b)), nil
}

func TestClientDrivesLanesProcess(t *testing.T) {
	p := newLanesProcess(t)
	c, err := NewClient(p.srv.URL, "tok", quiet)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx, 50*time.Millisecond)

	c.Apply(settings(), specs(t, "203.0.113.71", "203.0.113.72"))
	waitFor(t, "two usable lanes with known exits via the client", func() bool {
		u := c.Usable()
		return len(u) == 2 && u[0].ExitIP != "" && u[1].ExitIP != ""
	})
	for _, l := range c.Usable() {
		ip, err := exitIP(t, c, l.ID)
		if err != nil || ip != l.ExitIP {
			t.Fatalf("lane %s: exit %q (want %s) %v", l.ID, ip, l.ExitIP, err)
		}
	}
	if len(c.States()) != 2 {
		t.Fatalf("states %+v", c.States())
	}
	if ev := c.DrainEvents(); len(ev) == 0 {
		t.Fatal("no lane events forwarded")
	}

	if _, _, err := c.Dial(context.Background(), "test:nope", wgtest.WebAddr.String()+":80"); !errors.Is(err, lanes.ErrNoLane) {
		t.Fatalf("unknown lane: %v", err)
	}
	if err := c.Restart("test:0"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restarted lane back up", func() bool {
		for _, s := range c.States() {
			if s.ID == "test:0" && s.Restarts == 1 && s.Status == protocol.LaneUp {
				return true
			}
		}
		return false
	})

	// The lanes process restarts empty: the client sends the configuration again.
	p.restart()
	waitFor(t, "lanes back after a lanes-process restart", func() bool {
		return len(p.mgr.Load().States()) == 2 && len(c.Usable()) == 2
	})
}

func TestTokenRequired(t *testing.T) {
	p := newLanesProcess(t)
	bad, _ := NewClient(p.srv.URL, "wrong", quiet)
	bad.poll(context.Background())
	if bad.lastOK.IsZero() == false {
		t.Fatal("state served with a wrong token")
	}
	if _, _, err := bad.Dial(context.Background(), "test:0", "198.51.100.7:80"); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("CONNECT with a wrong token: %v", err)
	}
	resp, err := http.Get(p.srv.URL + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz: %v %v", resp, err)
	}
	resp.Body.Close()
}

func TestNewClientValidates(t *testing.T) {
	for _, u := range []string{"", "https://x:1", "://bad"} {
		if _, err := NewClient(u, "tok", quiet); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
	if _, err := NewClient("http://lanes:9191", "", quiet); err == nil {
		t.Error("empty token accepted")
	}
	c, err := NewClient("http://lanepool-lanes", "tok", quiet)
	if err != nil || c.host != "lanepool-lanes:9191" {
		t.Fatalf("default port: %+v %v", c, err)
	}
}
