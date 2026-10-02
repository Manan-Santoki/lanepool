package lanes

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/wg/wgtest"
)

// fast settings for tests; all durations are whole seconds in the protocol.
func testSettings() protocol.EngineSettings {
	s := protocol.DefaultSettings()
	s.LaneStartDelay = 0
	s.MaxConnecting = 5
	s.ConnectTimeout = 2
	s.RetryBackoff = 1
	s.RetryBackoffMax = 2
	s.BreakerFailures = 0
	s.IPCheckInterval = 600
	s.IPCheckURL = "http://api.ipify.org/"
	s.DialTimeout = 5
	return s
}

// laneFor builds a lane spec pointing at a fake provider.
func laneFor(id string, p *wgtest.Provider, keys ...string) protocol.LaneSpec {
	spec := protocol.LaneSpec{
		ID: id, Name: id, Provider: "wireguard", CountryCode: "US", Enabled: true,
		Endpoint: p.Endpoint, PeerKey: p.PublicKey,
		Addresses: []string{wgtest.ClientAddr.String()}, DNS: []string{wgtest.DNSAddr.String()},
	}
	for i, k := range keys {
		spec.Keys = append(spec.Keys, protocol.LaneKey{ID: int64(i + 1), PrivateKey: k})
	}
	return spec
}

func run(t *testing.T, m *Manager) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx, 50*time.Millisecond); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func state(m *Manager, id string) protocol.LaneState {
	for _, s := range m.States() {
		if s.ID == id {
			return s
		}
	}
	return protocol.LaneState{}
}

func TestLaneConnectsAndReportsExitIP(t *testing.T) {
	priv, pub := wgtest.KeyPair()
	p := wgtest.Start(t, "203.0.113.1", pub)
	m := New(testSettings(), nil)
	m.Apply(testSettings(), []protocol.LaneSpec{laneFor("a", p, priv)})
	run(t, m)

	waitFor(t, 5*time.Second, "lane up with exit IP", func() bool {
		s := state(m, "a")
		return s.Status == protocol.LaneUp && s.ExitIP == "203.0.113.1"
	})
	if s := state(m, "a"); s.LastHandshake == nil || s.KeyID != 1 {
		t.Fatalf("state %+v", s)
	}

	// Dial through the lane to the provider's echo service.
	conn, release, err := m.Dial(context.Background(), "a", net.JoinHostPort(wgtest.WebAddr.String(), "7"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(m.Usable()); got != 1 || m.Usable()[0].Active != 1 {
		t.Fatalf("usable %+v", m.Usable())
	}
	fmt.Fprint(conn, "ping")
	buf := make([]byte, 4)
	io.ReadFull(conn, buf)
	conn.Close()
	release()
	if string(buf) != "ping" || m.Usable()[0].Active != 0 {
		t.Fatalf("echo %q active %d", buf, m.Usable()[0].Active)
	}
}

func TestFailedLaneRetriesWithNextKey(t *testing.T) {
	badPriv, _ := wgtest.KeyPair()
	goodPriv, goodPub := wgtest.KeyPair()
	p := wgtest.Start(t, "203.0.113.2", goodPub) // only accepts the second key
	m := New(testSettings(), nil)
	m.Apply(testSettings(), []protocol.LaneSpec{laneFor("a", p, badPriv, goodPriv)})
	run(t, m)

	waitFor(t, 5*time.Second, "backoff", func() bool { return state(m, "a").Status == protocol.LaneBackoff })
	if s := state(m, "a"); s.KeyID != 2 || s.NextRetry == nil {
		t.Fatalf("after failure: %+v", s)
	}
	waitFor(t, 8*time.Second, "up with key 2", func() bool {
		s := state(m, "a")
		return s.Status == protocol.LaneUp && s.KeyID == 2
	})
	types := map[string]bool{}
	for _, e := range m.DrainEvents() {
		types[e.Type] = true
	}
	if !types[protocol.EventLaneBackoff] || !types[protocol.EventLaneUp] {
		t.Fatalf("events %v", types)
	}
}

func TestStartsAreGatedAndBreakerPauses(t *testing.T) {
	s := testSettings()
	s.MaxConnecting = 1
	s.BreakerFailures = 2
	s.BreakerPause = 600
	s.ConnectTimeout = 1

	var specs []protocol.LaneSpec
	for i := 0; i < 4; i++ {
		priv, _ := wgtest.KeyPair()
		_, otherPub := wgtest.KeyPair()
		p := wgtest.Start(t, "203.0.113.9", otherPub) // never accepts us
		specs = append(specs, laneFor(fmt.Sprintf("dead%d", i), p, priv))
	}
	m := New(s, nil)
	m.Apply(s, specs)
	run(t, m)

	peak := 0
	waitFor(t, 10*time.Second, "breaker to open", func() bool {
		n := 0
		for _, st := range m.States() {
			if st.Status == protocol.LaneConnecting {
				n++
			}
		}
		peak = max(peak, n)
		return !m.PausedUntil().IsZero()
	})
	if peak > 1 {
		t.Fatalf("%d lanes were connecting at once; limit is 1", peak)
	}
	// Two lanes failed, the other two must stay queued while paused.
	time.Sleep(1500 * time.Millisecond)
	queued := 0
	for _, st := range m.States() {
		if st.Status == protocol.LaneQueued {
			queued++
		}
	}
	if queued != 2 {
		t.Fatalf("%d lanes queued during the pause, want 2: %+v", queued, m.States())
	}
}

func TestApplyKeepsUnchangedLanesRunning(t *testing.T) {
	priv, pub := wgtest.KeyPair()
	p := wgtest.Start(t, "203.0.113.3", pub)
	m := New(testSettings(), nil)
	spec := laneFor("a", p, priv)
	m.Apply(testSettings(), []protocol.LaneSpec{spec})
	run(t, m)
	waitFor(t, 5*time.Second, "up", func() bool { return state(m, "a").Status == protocol.LaneUp })

	m.mu.Lock()
	before := m.lanes["a"].tun
	m.mu.Unlock()
	spec.Name = "renamed" // metadata only
	m.Apply(testSettings(), []protocol.LaneSpec{spec})
	m.mu.Lock()
	after := m.lanes["a"].tun
	m.mu.Unlock()
	if before != after {
		t.Fatal("unchanged lane was restarted")
	}

	// Adding a key (lane order of keys changes) must not reconnect the lane.
	extraPriv, _ := wgtest.KeyPair()
	spec.Keys = append([]protocol.LaneKey{{ID: 99, PrivateKey: extraPriv}}, spec.Keys...)
	m.Apply(testSettings(), []protocol.LaneSpec{spec})
	m.mu.Lock()
	afterKey := m.lanes["a"].tun
	m.mu.Unlock()
	if afterKey != before || state(m, "a").KeyID != 1 {
		t.Fatalf("adding a key reconnected the lane or changed its key: %+v", state(m, "a"))
	}

	// Removing the key in use does reconnect, with a remaining key.
	spec.Keys = spec.Keys[:1]
	m.Apply(testSettings(), []protocol.LaneSpec{spec})
	m.mu.Lock()
	afterRemove := m.lanes["a"].tun
	m.mu.Unlock()
	if afterRemove == before {
		t.Fatal("lane kept using a removed key")
	}

	spec.Enabled = false
	m.Apply(testSettings(), []protocol.LaneSpec{spec})
	if s := state(m, "a"); s.Status != protocol.LaneDisabled {
		t.Fatalf("disabled lane status %s", s.Status)
	}
	if _, _, err := m.Dial(context.Background(), "a", "198.51.100.7:7"); err != ErrNoLane {
		t.Fatalf("dial on disabled lane: %v", err)
	}
}

func TestRestartReconnects(t *testing.T) {
	priv, pub := wgtest.KeyPair()
	p := wgtest.Start(t, "203.0.113.4", pub)
	m := New(testSettings(), nil)
	m.Apply(testSettings(), []protocol.LaneSpec{laneFor("a", p, priv)})
	run(t, m)
	waitFor(t, 5*time.Second, "up", func() bool { return state(m, "a").Status == protocol.LaneUp })
	if err := m.Restart("a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Restart("missing"); err == nil {
		t.Fatal("restarting an unknown lane succeeded")
	}
	waitFor(t, 5*time.Second, "up again", func() bool {
		s := state(m, "a")
		return s.Status == protocol.LaneUp && s.Restarts == 1
	})
}

func TestBreakerProbesWithOneLaneAfterPause(t *testing.T) {
	s := testSettings()
	s.MaxConnecting = 3
	s.BreakerFailures = 2
	s.BreakerPause = 1
	s.ConnectTimeout = 1

	var specs []protocol.LaneSpec
	for i := 0; i < 6; i++ {
		priv, _ := wgtest.KeyPair()
		_, otherPub := wgtest.KeyPair()
		p := wgtest.Start(t, "203.0.113.9", otherPub) // never accepts us
		specs = append(specs, laneFor(fmt.Sprintf("dead%d", i), p, priv))
	}
	m := New(s, nil)
	m.Apply(s, specs)
	run(t, m)

	var events []protocol.Event
	opened := func() int {
		events = append(events, m.DrainEvents()...)
		n := 0
		for _, e := range events {
			if e.Type == protocol.EventBreakerOpen {
				n++
			}
		}
		return n
	}
	waitFor(t, 10*time.Second, "breaker to open", func() bool { return opened() >= 1 })
	peak := 0
	waitFor(t, 15*time.Second, "failed probe to reopen the breaker", func() bool {
		n := 0
		for _, st := range m.States() {
			if st.Status == protocol.LaneConnecting {
				n++
			}
		}
		peak = max(peak, n)
		return opened() >= 2
	})
	if peak > 1 {
		t.Fatalf("%d lanes connecting while probing; want 1", peak)
	}
	last := events[len(events)-1]
	for _, e := range events {
		if e.Type == protocol.EventBreakerOpen {
			last = e
		}
	}
	if want := "no new connections for 2s"; !strings.Contains(last.Message, want) {
		t.Fatalf("second breaker event %q, want %q", last.Message, want)
	}
	for _, st := range m.States() {
		if st.Status == protocol.LaneQueued && (st.NextRetry == nil || st.LastError == "") {
			t.Fatalf("queued lane during pause lacks retry info: %+v", st)
		}
	}
}

func TestPoolKeepsTargetUpAndSkipsFailingServers(t *testing.T) {
	s := testSettings()
	s.TargetUp = 2
	s.MaxConnecting = 2
	s.ConnectTimeout = 1
	s.RetryBackoff = 60
	s.RetryBackoffMax = 60

	priv, pub := wgtest.KeyPair()
	var specs []protocol.LaneSpec
	// Two dead servers first, then three that work.
	for i := 0; i < 2; i++ {
		_, otherPub := wgtest.KeyPair()
		specs = append(specs, laneFor(fmt.Sprintf("dead%d", i), wgtest.Start(t, "203.0.113.9", otherPub), priv))
	}
	for i := 0; i < 3; i++ {
		specs = append(specs, laneFor(fmt.Sprintf("ok%d", i), wgtest.Start(t, fmt.Sprintf("203.0.113.%d", 20+i), pub), priv))
	}
	m := New(s, nil)
	m.Apply(s, specs)
	run(t, m)

	count := func() (up, connecting, backoff int) {
		for _, st := range m.States() {
			switch st.Status {
			case protocol.LaneUp:
				up++
			case protocol.LaneConnecting:
				connecting++
			case protocol.LaneBackoff:
				backoff++
			}
		}
		return
	}
	waitFor(t, 10*time.Second, "two lanes up after skipping the dead servers", func() bool {
		up, connecting, _ := count()
		if up+connecting > 2 {
			t.Fatalf("%d lanes up or connecting; target is 2", up+connecting)
		}
		return up == 2
	})
	time.Sleep(500 * time.Millisecond)
	up, _, backoff := count()
	if up != 2 || backoff != 2 {
		t.Fatalf("up=%d backoff=%d, want 2 and 2: %+v", up, backoff, m.States())
	}
	if len(m.States()) != 5 {
		// ok2 never started: it is standby and not reported.
		if st := state(m, "ok2"); st.Status != "" {
			t.Fatalf("standby lane reported: %+v", st)
		}
	}

	// Lowering the target closes the surplus lane.
	s.TargetUp = 1
	m.Apply(s, specs)
	waitFor(t, 3*time.Second, "pool trimmed to one lane", func() bool { up, _, _ := count(); return up == 1 })
}
