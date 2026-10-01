// Package lanes runs the WireGuard tunnels ("lanes") and decides when they connect.
//
// Providers limit how fast an account or source IP may open new WireGuard
// sessions, and WireGuard has no disconnect, so sessions linger on the provider
// side after a restart. In production, opening ~99 sessions at once got the
// server's IP blocked for new sessions. The manager therefore:
//
//   - starts lanes one at a time (at most MaxConnecting at once, LaneStartDelay apart),
//   - switches a lane off when it can't connect and retries later with the next key,
//   - stops opening new sessions for BreakerPause after BreakerFailures failures in a row,
//   - never restarts a working lane unless its configuration changed or an admin asks.
package lanes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/wg"
)

// Tunnel is the part of wg.Tunnel the manager uses (replaceable in tests).
type Tunnel interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
	Stats() wg.Stats
	Close()
}

// StartFunc starts a tunnel.
type StartFunc func(ctx context.Context, cfg wg.Config) (Tunnel, error)

// DefaultStart starts real wireguard-go tunnels.
func DefaultStart(ctx context.Context, cfg wg.Config) (Tunnel, error) {
	return wg.Start(ctx, cfg, nil)
}

// ErrNoLane is returned when a lane isn't available for dialing.
var ErrNoLane = errors.New("lane not available")

type lane struct {
	spec    protocol.LaneSpec
	tun     Tunnel
	gen     int // incremented on every start; stale async results are ignored
	status  string
	keyIdx  int
	started time.Time
	lastUp  time.Time
	exitIP  string
	latency time.Duration

	nextStart    time.Time
	retryBackoff time.Duration
	restarts     int
	lastError    string
	nextIPCheck  time.Time
	checking     bool
	active       atomic.Int64
}

// Info is a snapshot of a usable lane, for the gateway's lane selection.
type Info struct {
	ID          string
	CountryCode string
	ExitIP      string
	Latency     time.Duration
	Active      int64
}

// Manager owns all lanes.
type Manager struct {
	start StartFunc
	now   func() time.Time

	mu          sync.Mutex
	settings    protocol.EngineSettings
	lanes       map[string]*lane
	order       []string
	nextLaunch  time.Time
	pausedUntil time.Time
	failures    int
	events      []protocol.Event
}

// New creates a manager. start may be nil for real tunnels.
func New(settings protocol.EngineSettings, start StartFunc) *Manager {
	if start == nil {
		start = DefaultStart
	}
	return &Manager{start: start, now: time.Now, settings: settings, lanes: map[string]*lane{}}
}

func sec(n int) time.Duration { return time.Duration(n) * time.Second }

// Apply updates settings and lane definitions. Lanes whose WireGuard settings
// didn't change keep running; changed lanes are restarted (paced); removed or
// disabled lanes are closed.
func (m *Manager) Apply(settings protocol.EngineSettings, specs []protocol.LaneSpec) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = settings
	seen := map[string]bool{}
	order := make([]string, 0, len(specs))
	for _, spec := range specs {
		seen[spec.ID] = true
		order = append(order, spec.ID)
		l, ok := m.lanes[spec.ID]
		switch {
		case !ok:
			l = &lane{spec: spec, status: protocol.LaneQueued}
			if !spec.Enabled {
				l.status = protocol.LaneDisabled
			}
			m.lanes[spec.ID] = l
		case !spec.Enabled:
			m.stop(l)
			l.spec = spec
			l.status = protocol.LaneDisabled
		case tunnelChanged(l.spec, spec):
			m.stop(l)
			l.spec = spec
			l.keyIdx = 0
			l.status = protocol.LaneQueued
			l.nextStart = time.Time{}
		default:
			l.spec = spec // metadata only (name, country)
			if l.status == protocol.LaneDisabled {
				l.status = protocol.LaneQueued
			}
		}
		if l.keyIdx >= len(spec.Keys) {
			l.keyIdx = 0
		}
	}
	for id, l := range m.lanes {
		if !seen[id] {
			m.stop(l)
			delete(m.lanes, id)
		}
	}
	m.order = order
}

func tunnelChanged(a, b protocol.LaneSpec) bool {
	if a.Endpoint != b.Endpoint || a.PeerKey != b.PeerKey || a.PresharedKey != b.PresharedKey ||
		a.MTU != b.MTU || strings.Join(a.Addresses, ",") != strings.Join(b.Addresses, ",") ||
		strings.Join(a.DNS, ",") != strings.Join(b.DNS, ",") || len(a.Keys) != len(b.Keys) {
		return true
	}
	for i := range a.Keys {
		if a.Keys[i] != b.Keys[i] {
			return true
		}
	}
	return false
}

// stop closes a lane's tunnel. Caller holds m.mu.
func (m *Manager) stop(l *lane) {
	if l.tun != nil {
		l.tun.Close()
		l.tun = nil
	}
	l.gen++
	l.exitIP = ""
}

// Run drives the lanes until ctx is cancelled, then closes every tunnel.
func (m *Manager) Run(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		m.Tick(ctx)
		select {
		case <-ctx.Done():
			m.mu.Lock()
			for _, l := range m.lanes {
				m.stop(l)
			}
			m.mu.Unlock()
			return
		case <-t.C:
		}
	}
}

// Tick updates lane states, starts due lanes and schedules exit-IP checks.
func (m *Manager) Tick(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	s := m.settings
	for _, id := range m.order {
		l := m.lanes[id]
		if l.tun == nil {
			continue
		}
		hs := l.tun.Stats().LastHandshake
		fresh := !hs.IsZero() && now.Sub(hs) < sec(s.HandshakeMaxAge)
		switch {
		case fresh && l.status != protocol.LaneUp:
			l.status = protocol.LaneUp
			l.lastUp = now
			l.lastError = ""
			l.retryBackoff = 0
			l.nextIPCheck = now
			m.failures = 0
			m.event("info", protocol.EventLaneUp, l.spec.ID, fmt.Sprintf("connected with key %d", l.keyID()))
		case fresh:
			l.lastUp = now
		case l.status == protocol.LaneUp:
			l.status = protocol.LaneDown
			l.lastError = "handshakes stopped"
			m.event("warn", protocol.EventLaneDown, l.spec.ID, "handshakes stopped")
		case now.Sub(maxTime(l.started, l.lastUp)) > sec(s.ConnectTimeout):
			m.park(l, now)
		}
	}
	m.launch(ctx, now)
	m.scheduleIPChecks(ctx, now)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (l *lane) keyID() int64 {
	if l.keyIdx < len(l.spec.Keys) {
		return l.spec.Keys[l.keyIdx].ID
	}
	return 0
}

// launch starts due lanes, slowly. Caller holds m.mu.
func (m *Manager) launch(ctx context.Context, now time.Time) {
	s := m.settings
	if now.Before(m.pausedUntil) || now.Before(m.nextLaunch) {
		return
	}
	connecting := 0
	var due []*lane
	for _, id := range m.order {
		l := m.lanes[id]
		if l.status == protocol.LaneConnecting {
			connecting++
		}
		if l.tun == nil && l.status != protocol.LaneDisabled && l.status != protocol.LaneConnecting && !now.Before(l.nextStart) && len(l.spec.Keys) > 0 {
			due = append(due, l)
		}
	}
	sort.SliceStable(due, func(i, j int) bool { return due[i].nextStart.Before(due[j].nextStart) })
	for _, l := range due {
		if connecting >= max(1, s.MaxConnecting) {
			return
		}
		m.startLane(ctx, l, now)
		connecting++
		m.nextLaunch = now.Add(sec(s.LaneStartDelay))
		if s.LaneStartDelay > 0 {
			return // one start per delay period
		}
	}
}

// startLane starts the tunnel in the background. Caller holds m.mu.
func (m *Manager) startLane(ctx context.Context, l *lane, now time.Time) {
	l.gen++
	gen := l.gen
	l.status = protocol.LaneConnecting
	l.started = now
	l.lastUp = time.Time{}
	cfg, err := tunnelConfig(l.spec, l.spec.Keys[l.keyIdx].PrivateKey)
	if err != nil {
		l.lastError = err.Error()
		m.park(l, now)
		return
	}
	timeout := sec(m.settings.ConnectTimeout)
	go func() {
		// Bounds endpoint DNS resolution; the handshake itself is watched by Tick.
		startCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		tun, err := m.start(startCtx, cfg)
		m.mu.Lock()
		defer m.mu.Unlock()
		if l.gen != gen || l.status != protocol.LaneConnecting {
			if tun != nil {
				tun.Close() // restarted or removed meanwhile
			}
			return
		}
		if err != nil {
			l.lastError = err.Error()
			m.park(l, m.now())
			return
		}
		l.tun = tun
	}()
}

func tunnelConfig(spec protocol.LaneSpec, priv string) (wg.Config, error) {
	cfg := wg.Config{PrivateKey: priv, PeerKey: spec.PeerKey, PresharedKey: spec.PresharedKey, Endpoint: spec.Endpoint, MTU: spec.MTU}
	for _, a := range spec.Addresses {
		p, err := netip.ParsePrefix(a)
		if err != nil {
			addr, err2 := netip.ParseAddr(a)
			if err2 != nil {
				return cfg, fmt.Errorf("address %q: %w", a, err)
			}
			cfg.Addresses = append(cfg.Addresses, addr)
			continue
		}
		cfg.Addresses = append(cfg.Addresses, p.Addr())
	}
	for _, d := range spec.DNS {
		addr, err := netip.ParseAddr(d)
		if err != nil {
			return cfg, fmt.Errorf("dns %q: %w", d, err)
		}
		cfg.DNS = append(cfg.DNS, addr)
	}
	return cfg, nil
}

// park switches off a lane that can't connect. It is retried after a growing
// delay with the next key. Caller holds m.mu.
func (m *Manager) park(l *lane, now time.Time) {
	s := m.settings
	m.stop(l)
	l.retryBackoff = min(max(l.retryBackoff*2, sec(s.RetryBackoff)), sec(s.RetryBackoffMax))
	l.status = protocol.LaneBackoff
	l.nextStart = now.Add(l.retryBackoff)
	l.restarts++
	reason := l.lastError
	if reason == "" {
		reason = fmt.Sprintf("no handshake within %ds", s.ConnectTimeout)
	}
	msg := fmt.Sprintf("%s; retrying in %s", reason, l.retryBackoff.Round(time.Second))
	if len(l.spec.Keys) > 1 {
		l.keyIdx = (l.keyIdx + 1) % len(l.spec.Keys)
		msg += fmt.Sprintf(" with key %d", l.keyID())
	}
	l.lastError = msg
	m.event("warn", protocol.EventLaneBackoff, l.spec.ID, msg)

	m.failures++
	if s.BreakerFailures > 0 && m.failures >= s.BreakerFailures && s.BreakerPause > 0 {
		m.pausedUntil = now.Add(sec(s.BreakerPause))
		m.failures = 0
		m.event("warn", protocol.EventBreakerOpen, "", fmt.Sprintf(
			"%d lanes failed to connect in a row; no new connections for %s",
			s.BreakerFailures, sec(s.BreakerPause)))
	}
}

func (m *Manager) event(level, typ, laneID, msg string) {
	m.events = append(m.events, protocol.Event{Time: m.now(), Level: level, Type: typ, LaneID: laneID, Message: msg})
	if len(m.events) > 1000 {
		m.events = m.events[len(m.events)-1000:]
	}
}

// DrainEvents returns and clears the events since the last call.
func (m *Manager) DrainEvents() []protocol.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	ev := m.events
	m.events = nil
	return ev
}

// Restart reconnects one lane now (admin action). It also lifts a breaker pause.
func (m *Manager) Restart(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.lanes[id]
	if !ok {
		return fmt.Errorf("unknown lane %q", id)
	}
	if l.status == protocol.LaneDisabled {
		return fmt.Errorf("lane %q is disabled", id)
	}
	m.stop(l)
	l.status = protocol.LaneQueued
	l.nextStart = time.Time{}
	l.retryBackoff = 0
	l.restarts++
	m.pausedUntil = time.Time{}
	m.nextLaunch = time.Time{}
	return nil
}

// RestartAll reconnects every enabled lane. They are queued and come back paced.
func (m *Manager) RestartAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range m.lanes {
		if l.status == protocol.LaneDisabled {
			continue
		}
		m.stop(l)
		l.status = protocol.LaneQueued
		l.nextStart = time.Time{}
		l.retryBackoff = 0
		l.restarts++
	}
	m.pausedUntil = time.Time{}
}

// PausedUntil reports when the circuit breaker closes again (zero if it isn't open).
func (m *Manager) PausedUntil() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.now().After(m.pausedUntil) {
		return time.Time{}
	}
	return m.pausedUntil
}

// Usable lists lanes that are up, for lane selection.
func (m *Manager) Usable() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.order))
	for _, id := range m.order {
		l := m.lanes[id]
		if l.status == protocol.LaneUp && l.tun != nil {
			out = append(out, Info{ID: id, CountryCode: strings.ToLower(l.spec.CountryCode), ExitIP: l.exitIP, Latency: l.latency, Active: l.active.Load()})
		}
	}
	return out
}

// Dial opens a connection to address through lane id. The returned release
// function must be called when the connection is closed.
func (m *Manager) Dial(ctx context.Context, id, address string) (net.Conn, func(), error) {
	m.mu.Lock()
	l, ok := m.lanes[id]
	var tun Tunnel
	if ok && l.status == protocol.LaneUp {
		tun = l.tun
	}
	m.mu.Unlock()
	if tun == nil {
		return nil, nil, ErrNoLane
	}
	l.active.Add(1)
	conn, err := tun.DialContext(ctx, "tcp", address)
	if err != nil {
		l.active.Add(-1)
		return nil, nil, err
	}
	var once sync.Once
	return conn, func() { once.Do(func() { l.active.Add(-1) }) }, nil
}

// ExitIP returns the last known exit IP of a lane.
func (m *Manager) ExitIP(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.lanes[id]; ok {
		return l.exitIP
	}
	return ""
}

// States returns the runtime state of every lane, in order.
func (m *Manager) States() []protocol.LaneState {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]protocol.LaneState, 0, len(m.order))
	for _, id := range m.order {
		l := m.lanes[id]
		st := protocol.LaneState{
			ID: id, Status: l.status, KeyID: l.keyID(), ExitIP: l.exitIP,
			LatencyMs: int(l.latency / time.Millisecond), ActiveConnections: int(l.active.Load()),
			Restarts: l.restarts, LastError: l.lastError,
		}
		if l.tun != nil {
			stats := l.tun.Stats()
			st.RxBytes, st.TxBytes = stats.RxBytes, stats.TxBytes
			if !stats.LastHandshake.IsZero() {
				hs := stats.LastHandshake
				st.LastHandshake = &hs
			}
		}
		if l.status == protocol.LaneBackoff {
			ns := l.nextStart
			st.NextRetry = &ns
		}
		out = append(out, st)
	}
	return out
}

// --- exit IP checks ---------------------------------------------------------

// scheduleIPChecks looks up the public IP of lanes that are due. Caller holds m.mu.
func (m *Manager) scheduleIPChecks(ctx context.Context, now time.Time) {
	s := m.settings
	if s.IPCheckInterval <= 0 || s.IPCheckURL == "" {
		return
	}
	for _, id := range m.order {
		l := m.lanes[id]
		if l.status != protocol.LaneUp || l.tun == nil || l.checking || now.Before(l.nextIPCheck) {
			continue
		}
		l.checking = true
		gen, tun := l.gen, l.tun
		go func() {
			ip, latency, err := checkIP(ctx, tun, s.IPCheckURL, sec(s.DialTimeout))
			m.mu.Lock()
			defer m.mu.Unlock()
			l.checking = false
			if l.gen != gen {
				return
			}
			if err != nil {
				l.nextIPCheck = m.now().Add(time.Minute)
				return
			}
			l.nextIPCheck = m.now().Add(sec(s.IPCheckInterval))
			l.latency = latency
			if ip != l.exitIP {
				if l.exitIP != "" {
					m.event("info", protocol.EventExitIPChanged, l.spec.ID, fmt.Sprintf("exit IP %s -> %s", l.exitIP, ip))
				}
				l.exitIP = ip
			}
		}()
	}
}

func checkIP(ctx context.Context, tun Tunnel, url string, timeout time.Duration) (string, time.Duration, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client := http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: tun.DialContext, DisableKeepAlives: true},
	}
	started := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", 0, err
	}
	ip := strings.TrimSpace(string(body))
	if _, err := netip.ParseAddr(ip); err != nil || resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("unexpected IP check response (HTTP %d): %q", resp.StatusCode, ip)
	}
	return ip, time.Since(started), nil
}
