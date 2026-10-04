package lanes

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/wg"
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

// Pool tests use a controlled clock: production pacing must remain enforced even
// when settings ask for immediate parallel starts.
type poolTunnel struct {
	now     func() time.Time
	healthy atomic.Bool
	closed  atomic.Bool
	dials   atomic.Int64
}

func (t *poolTunnel) Stats() wg.Stats {
	if t.healthy.Load() && !t.closed.Load() {
		return wg.Stats{LastHandshake: t.now()}
	}
	return wg.Stats{}
}
func (t *poolTunnel) Close() { t.closed.Store(true) }
func (t *poolTunnel) DialContext(context.Context, string, string) (net.Conn, error) {
	t.dials.Add(1)
	return nil, fmt.Errorf("test destination unavailable")
}

type poolHarness struct {
	m      *Manager
	clock  atomic.Int64
	starts atomic.Int64
}

func newPoolHarness(t *testing.T, s protocol.EngineSettings, specs []protocol.LaneSpec) *poolHarness {
	t.Helper()
	h := &poolHarness{}
	h.clock.Store(time.Now().UnixNano())
	now := func() time.Time { return time.Unix(0, h.clock.Load()) }
	h.m = New(s, func(_ context.Context, cfg wg.Config) (Tunnel, error) {
		h.starts.Add(1)
		tun := &poolTunnel{now: now}
		tun.healthy.Store(!strings.HasPrefix(cfg.Endpoint, "dead"))
		return tun, nil
	})
	h.m.now = now
	h.m.Apply(s, specs)
	t.Cleanup(func() {
		h.m.mu.Lock()
		defer h.m.mu.Unlock()
		for _, l := range h.m.lanes {
			h.m.stop(l)
		}
	})
	return h
}

func poolSpecs(ids ...string) []protocol.LaneSpec {
	var specs []protocol.LaneSpec
	for _, id := range ids {
		specs = append(specs, protocol.LaneSpec{ID: id, Enabled: true, Endpoint: id + ":51820",
			Addresses: []string{"10.0.0.2"}, Keys: []protocol.LaneKey{{ID: 1, PrivateKey: "test"}}})
	}
	return specs
}

func (h *poolHarness) tick(t *testing.T, advance time.Duration) {
	t.Helper()
	h.clock.Add(int64(advance))
	h.m.Tick(context.Background())
	waitFor(t, time.Second, "tunnel start to finish", func() bool {
		h.m.mu.Lock()
		defer h.m.mu.Unlock()
		for _, l := range h.m.lanes {
			if l.status == protocol.LaneConnecting && l.tun == nil {
				return false
			}
		}
		return true
	})
	h.m.Tick(context.Background())
}

func TestPoolKeepsTargetUpAndSkipsFailingServers(t *testing.T) {
	s := testSettings()
	s.TargetUp = 2
	s.IPCheckInterval = 0
	s.RetryBackoff = 3600
	s.RetryBackoffMax = 3600
	specs := poolSpecs("dead0", "dead1", "ok0", "ok1", "ok2")
	h := newPoolHarness(t, s, specs)
	h.tick(t, 0)
	h.tick(t, 3*time.Second) // first handshake times out
	h.tick(t, time.Minute)
	h.tick(t, 3*time.Second) // second handshake times out
	h.tick(t, time.Minute)
	h.tick(t, time.Minute)
	if len(h.m.Usable()) != 2 || h.starts.Load() != 4 {
		t.Fatalf("usable=%+v starts=%d", h.m.Usable(), h.starts.Load())
	}
	h.tick(t, time.Hour)
	if h.starts.Load() != 4 || state(h.m, "ok2").Status != "" {
		t.Fatal("full pool started a standby or retry lane")
	}
	s.TargetUp = 1
	h.m.Apply(s, specs)
	h.tick(t, 0)
	if len(h.m.Usable()) != 1 {
		t.Fatal("pool did not trim to its new target")
	}
}

func TestPoolSerializesAndPacesAllCandidatesAndKeys(t *testing.T) {
	s := testSettings()
	s.TargetUp = 30
	s.IPCheckInterval = 0
	specs := poolSpecs("dead0", "ok1", "ok2")
	specs[1].Keys[0].ID = 45
	h := newPoolHarness(t, s, specs)
	h.tick(t, 0)
	h.tick(t, time.Second)
	if h.starts.Load() != 1 {
		t.Fatal("started overlapping handshakes")
	}
	h.tick(t, 2*time.Second) // failed attempt reserves a fresh shared minute
	h.m.Apply(s, specs)      // reloading config must not reset the budget
	h.tick(t, time.Minute-time.Second)
	if h.starts.Load() != 1 {
		t.Fatal("another server/key bypassed the failure cooldown")
	}
	h.tick(t, time.Second)
	if h.starts.Load() != 2 || len(h.m.Usable()) != 1 {
		t.Fatal("eligible replacement did not start")
	}
	h.tick(t, time.Minute-time.Second)
	if h.starts.Load() != 2 {
		t.Fatal("success bypassed the shared start interval")
	}
	h.tick(t, time.Second)
	if h.starts.Load() != 3 {
		t.Fatal("pool failed to continue filling")
	}
}

func TestPoolFailureBudgetSurvivesSuccessAndPreservesHealthyTunnels(t *testing.T) {
	s := testSettings()
	s.TargetUp = 30
	s.IPCheckInterval = 0
	s.BreakerFailures = 2
	s.BreakerPause = 900
	s.RetryBackoff = 3600
	s.RetryBackoffMax = 3600
	h := newPoolHarness(t, s, poolSpecs("dead0", "ok1", "dead2", "dead3", "ok4"))
	h.tick(t, 0)
	h.tick(t, 3*time.Second)
	h.tick(t, time.Minute) // a success between the two failures
	healthy := h.m.lanes["ok1"].tun.(*poolTunnel)
	h.tick(t, time.Minute)
	h.tick(t, 3*time.Second)
	if h.m.PausedUntil().IsZero() {
		t.Fatal("intermittent success erased pool failures")
	}
	if healthy.closed.Load() || len(h.m.Usable()) != 1 {
		t.Fatal("breaker disrupted a healthy tunnel")
	}
	if err := h.m.Restart("dead0"); err != nil {
		t.Fatal(err)
	}
	h.tick(t, 899*time.Second)
	if h.starts.Load() != 3 {
		t.Fatal("manual restart bypassed pool cooldown")
	}
	h.tick(t, time.Second) // exactly one half-open probe
	if h.starts.Load() != 4 {
		t.Fatal("missing half-open probe")
	}
	h.tick(t, 3*time.Second)
	if pause := h.m.PausedUntil().Sub(h.m.now()); pause != 1800*time.Second {
		t.Fatalf("failed probe pause=%s, want 30m", pause)
	}
	if healthy.closed.Load() {
		t.Fatal("failed probe closed a healthy tunnel")
	}
}

func TestPoolProbeSuccessRestoresBudget(t *testing.T) {
	s := testSettings()
	s.TargetUp = 2
	s.IPCheckInterval = 0
	s.BreakerFailures = 1
	s.BreakerPause = 900
	s.RetryBackoff = 3600
	s.RetryBackoffMax = 3600
	h := newPoolHarness(t, s, poolSpecs("dead0", "ok1", "ok2"))
	h.tick(t, 0)
	h.tick(t, 3*time.Second)
	h.tick(t, 900*time.Second)
	if h.m.trips != 0 || len(h.m.poolFailures) != 0 || len(h.m.Usable()) != 1 {
		t.Fatal("successful probe did not restore pool budget")
	}
	h.tick(t, time.Minute)
	if len(h.m.Usable()) != 2 {
		t.Fatal("pool did not resume filling after successful probe")
	}
}

func TestPoolFailureWindowExpires(t *testing.T) {
	s := testSettings()
	s.TargetUp = 3
	s.IPCheckInterval = 0
	s.BreakerFailures = 2
	s.BreakerPause = 900
	s.RetryBackoff = 3600
	s.RetryBackoffMax = 3600
	h := newPoolHarness(t, s, poolSpecs("dead0", "dead1", "ok2"))
	h.tick(t, 0)
	h.tick(t, 3*time.Second)
	h.tick(t, 901*time.Second)
	h.tick(t, 3*time.Second)
	if !h.m.PausedUntil().IsZero() || len(h.m.poolFailures) != 1 {
		t.Fatal("expired failures counted against the current pool budget")
	}
}

func TestPoolIPLookupIsOncePerTunnelEvenOnFailure(t *testing.T) {
	s := testSettings()
	s.TargetUp = 1
	h := newPoolHarness(t, s, poolSpecs("ok0"))
	h.tick(t, 0)
	tun := h.m.lanes["ok0"].tun.(*poolTunnel)
	waitFor(t, time.Second, "initial IP lookup", func() bool {
		h.m.mu.Lock()
		defer h.m.mu.Unlock()
		return !h.m.lanes["ok0"].checking && tun.dials.Load() == 1
	})
	h.tick(t, 24*time.Hour)
	if tun.dials.Load() != 1 || tun.closed.Load() {
		t.Fatal("pool repeated synthetic probes or restarted a healthy tunnel")
	}
}

func TestPoolPrefersPreviouslyWorkingBackup(t *testing.T) {
	s := testSettings()
	s.TargetUp = 2
	s.IPCheckInterval = 0
	specs := poolSpecs("ok0", "ok1", "ok2")
	h := newPoolHarness(t, s, specs)
	h.tick(t, 0)
	h.tick(t, time.Minute)
	s.TargetUp = 1
	h.m.Apply(s, specs)
	h.tick(t, 0) // ok1 becomes a disconnected, previously working backup
	h.m.mu.Lock()
	tun := h.m.lanes["ok0"].tun.(*poolTunnel)
	tun.healthy.Store(false)
	h.m.mu.Unlock()
	h.tick(t, 181*time.Second)
	h.tick(t, 3*time.Second)
	h.tick(t, time.Minute)
	if state(h.m, "ok1").Status != protocol.LaneUp || state(h.m, "ok2").Status != "" {
		t.Fatal("pool preferred an unproven candidate over its working backup")
	}
}

func TestPoolInvalidConfigCannotBypassBudget(t *testing.T) {
	s := testSettings()
	s.TargetUp = 30
	s.IPCheckInterval = 0
	s.BreakerFailures = 2
	s.BreakerPause = 900
	specs := poolSpecs("bad0", "ok1")
	specs[0].Addresses = []string{"invalid"}
	h := newPoolHarness(t, s, specs)
	h.tick(t, 0)
	if state(h.m, "bad0").Status != protocol.LaneBackoff || h.starts.Load() != 0 {
		t.Fatal("invalid lane did not fail before tunnel startup")
	}
	h.tick(t, time.Minute-time.Second)
	if h.starts.Load() != 0 {
		t.Fatal("invalid configuration bypassed the shared interval")
	}
	h.tick(t, time.Second)
	if h.starts.Load() != 1 || len(h.m.Usable()) != 1 {
		t.Fatal("valid replacement did not start after cooldown")
	}
}

func TestPoolRestartAllKeepsFailureBudget(t *testing.T) {
	s := testSettings()
	s.TargetUp = 30
	s.IPCheckInterval = 0
	s.BreakerFailures = 1
	s.BreakerPause = 900
	h := newPoolHarness(t, s, poolSpecs("dead0", "ok1"))
	h.tick(t, 0)
	h.tick(t, 3*time.Second)
	before := h.m.PausedUntil()
	h.m.RestartAll()
	h.tick(t, 899*time.Second)
	if h.starts.Load() != 1 || h.m.PausedUntil() != before || h.m.trips != 1 {
		t.Fatal("restart-all bypassed the shared breaker")
	}
}

func TestPoolLateSuccessCannotResetAnOpenBreaker(t *testing.T) {
	s := testSettings()
	s.TargetUp = 30
	s.IPCheckInterval = 0
	h := newPoolHarness(t, s, poolSpecs("ok0", "ok1"))
	// A connection started before the breaker opened can finish during the
	// cooldown (for example, after switching from non-pool parallel starts).
	h.m.mu.Lock()
	l := h.m.lanes["ok0"]
	tun := &poolTunnel{now: h.m.now}
	tun.healthy.Store(true)
	l.tun, l.status, l.started = tun, protocol.LaneConnecting, h.m.now()
	h.m.trips = 1
	h.m.poolFailures = []time.Time{h.m.now()}
	h.m.pausedUntil = h.m.now().Add(15 * time.Minute)
	h.m.mu.Unlock()
	h.tick(t, time.Minute)
	if len(h.m.Usable()) != 1 || h.m.trips != 1 || len(h.m.poolFailures) != 1 || h.starts.Load() != 0 {
		t.Fatal("late success bypassed the open pool breaker")
	}
}
