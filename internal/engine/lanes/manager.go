// Package lanes runs the WireGuard tunnels ("lanes") and decides when they connect.
//
// Providers limit how fast an account or source IP may open new WireGuard
// sessions, and WireGuard has no disconnect, so sessions linger on the provider
// side after a restart. In production, opening ~99 sessions at once got the
// server's IP blocked for new sessions. Outside pool mode, the manager:
//
//   - starts lanes one at a time (at most MaxConnecting at once, LaneStartDelay apart),
//   - switches a lane off when it can't connect and retries later with the next key,
//   - stops opening new sessions for BreakerPause after BreakerFailures failures in a row,
//     then probes with a single lane; each failed probe doubles the pause (up to 4×),
//   - never restarts a working lane unless its configuration changed or an admin asks.
//
// With TargetUp set, the lanes are a pool of candidate servers: the manager keeps
// TargetUp of them up and leaves the rest on standby. When a lane fails, it backs
// off and the next untried candidate starts in its place.
// A pool starts with up to three parallel bursts, each limited to its missing
// target slots. It then fills remaining slots one at a time, backing off by
// 30 seconds after each failed attempt (60s, 90s, 120s, ...).
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

const (
	poolStartInterval = time.Minute
	poolRetryStep     = 30 * time.Second
	poolBurstRounds   = 3
)

type lane struct {
	spec    protocol.LaneSpec
	tun     Tunnel
	gen     int // incremented on every start; stale async results are ignored
	status  string
	keyIdx  int
	started time.Time
	lastUp  time.Time
	wasUp   bool
	burst   bool // this connection attempt belongs to an initial pool burst
	exitIP  string
	latency time.Duration

	nextStart    time.Time
	retryBackoff time.Duration
	restarts     int
	lastError    string
	nextIPCheck  time.Time
	checking     bool
	ipChecked    bool
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
	trips       int // non-pool breaker openings since the last successful connection
	burstRounds int
	burstActive bool
	fallback    bool
	poolDelay   time.Duration
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
		case tunnelChanged(l.spec, spec) || !hasKey(spec.Keys, l.keyID()):
			// Peer settings changed, or the key in use was removed: reconnect.
			m.stop(l)
			l.spec = spec
			l.keyIdx = 0
			if l.status != protocol.LaneBackoff {
				l.status = protocol.LaneQueued
				l.nextStart = time.Time{}
				l.lastError = ""
			}
		default:
			// Same peer and the current key is still allowed: keep the tunnel and
			// just follow the key's new position in the list.
			cur := l.keyID()
			l.spec = spec
			for i, k := range spec.Keys {
				if k.ID == cur {
					l.keyIdx = i
				}
			}
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

// tunnelChanged reports whether the WireGuard peer settings differ. Key list
// changes are handled separately so that adding a key doesn't reconnect lanes.
func tunnelChanged(a, b protocol.LaneSpec) bool {
	return a.Endpoint != b.Endpoint || a.PeerKey != b.PeerKey || a.PresharedKey != b.PresharedKey ||
		a.MTU != b.MTU || strings.Join(a.Addresses, ",") != strings.Join(b.Addresses, ",") ||
		strings.Join(a.DNS, ",") != strings.Join(b.DNS, ",")
}

// hasKey reports whether keys contains id; lanes without keys yet (id 0) count as present.
func hasKey(keys []protocol.LaneKey, id int64) bool {
	if id == 0 {
		return true
	}
	for _, k := range keys {
		if k.ID == id {
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
	l.checking = false
	l.ipChecked = false
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
			newConnection := l.status == protocol.LaneConnecting
			l.status = protocol.LaneUp
			l.wasUp = true
			l.lastUp = now
			l.lastError = ""
			l.retryBackoff = 0
			l.nextIPCheck = now
			if s.TargetUp <= 0 {
				m.failures = 0
				m.trips = 0
			} else if newConnection && !l.burst {
				// A successful fallback attempt restores the base delay without
				// rearming the startup bursts or reconnecting healthy tunnels.
				m.poolDelay = poolStartInterval
				m.nextLaunch = now.Add(m.fallbackDelay())
			}
			l.burst = false
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
	m.trimPool()
	m.launch(ctx, now)
	m.scheduleIPChecks(ctx, now)
}

// trimPool keeps established tunnels before pending attempts. A recovering old
// tunnel must not be dropped just because its replacement is still connecting.
// Caller holds m.mu.
func (m *Manager) trimPool() {
	target := m.settings.TargetUp
	if target <= 0 {
		return
	}
	var active []*lane
	for _, id := range m.order {
		l := m.lanes[id]
		if l.status == protocol.LaneUp || l.status == protocol.LaneConnecting {
			active = append(active, l)
		}
	}
	if len(active) <= target {
		return
	}
	sort.SliceStable(active, func(i, j int) bool {
		if (active[i].status == protocol.LaneUp) != (active[j].status == protocol.LaneUp) {
			return active[i].status == protocol.LaneUp
		}
		return active[i].started.Before(active[j].started)
	})
	for _, l := range active[target:] {
		m.stop(l)
		l.status = protocol.LaneQueued
		l.burst = false
		l.nextStart = time.Time{}
	}
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

// fallbackDelay is the shared pool delay, respecting a larger configured delay.
// Caller holds m.mu.
func (m *Manager) fallbackDelay() time.Duration {
	return max(m.poolDelay, poolStartInterval, sec(m.settings.LaneStartDelay))
}

func (m *Manager) startFallback(now time.Time, up int) {
	m.fallback = true
	m.burstActive = false
	m.poolDelay = poolStartInterval
	m.nextLaunch = maxTime(m.nextLaunch, now.Add(m.fallbackDelay()))
	m.event("info", "pool.fallback", "", fmt.Sprintf("%d/%d lanes up after %d bursts; remaining slots use single attempts after %s", up, m.settings.TargetUp, m.burstRounds, m.fallbackDelay()))
}

// launch starts a pool in bursts, then uses one shared fallback schedule. Non-pool
// lanes retain their configured pacing and circuit breaker. Caller holds m.mu.
func (m *Manager) launch(ctx context.Context, now time.Time) {
	s := m.settings
	pool := s.TargetUp > 0
	connecting, up := 0, 0
	for _, id := range m.order {
		switch m.lanes[id].status {
		case protocol.LaneConnecting:
			connecting++
		case protocol.LaneUp:
			up++
		}
	}
	if pool {
		if m.burstActive {
			// Settle every result before another wave.
			if connecting > 0 {
				return
			}
			m.burstActive = false
		}
		if up >= s.TargetUp {
			// Replacements and later target increases use fallback, even when the
			// target was reached before all three startup rounds were needed.
			if !m.fallback {
				m.startFallback(now, up)
			}
			return
		}
		if !m.fallback && m.burstRounds >= poolBurstRounds {
			m.startFallback(now, up)
		}
	}
	if now.Before(m.pausedUntil) || now.Before(m.nextLaunch) {
		return
	}
	burst := pool && !m.fallback
	limit := max(1, s.MaxConnecting)
	delay := sec(s.LaneStartDelay)
	if burst {
		if connecting > 0 {
			return
		}
		limit = s.TargetUp - up
		delay = 0
	} else if pool {
		limit = 1
		delay = m.fallbackDelay()
	} else if m.trips > 0 {
		limit = 1
	}
	var due []*lane
	for _, id := range m.order {
		l := m.lanes[id]
		if l.tun == nil && l.status != protocol.LaneDisabled && l.status != protocol.LaneConnecting &&
			(burst || !now.Before(l.nextStart)) && len(l.spec.Keys) > 0 {
			due = append(due, l)
		}
	}
	// Reuse known working backups, then untried candidates, then failed servers.
	sort.SliceStable(due, func(i, j int) bool {
		if pool && due[i].wasUp != due[j].wasUp {
			return due[i].wasUp
		}
		return due[i].nextStart.Before(due[j].nextStart)
	})
	if burst && len(due) > 0 {
		m.burstRounds++
		m.burstActive = true
		m.event("info", "pool.burst", "", fmt.Sprintf("connection burst %d/%d: trying up to %d servers with %d/%d lanes already up", m.burstRounds, poolBurstRounds, min(limit, len(due)), up, s.TargetUp))
	}
	for _, l := range due {
		if connecting >= limit || (pool && up+connecting >= s.TargetUp) {
			return
		}
		m.nextLaunch = now.Add(delay)
		l.burst = burst
		m.startLane(ctx, l, now)
		connecting++
		if delay > 0 {
			return
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
	l.lastError = ""
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
	pool, burst, connecting := s.TargetUp > 0, l.burst, l.status == protocol.LaneConnecting
	m.stop(l)
	if pool {
		if burst {
			// Initial rounds may retry failed candidates immediately, but only
			// after the rest of the wave has settled.
			l.retryBackoff = 0
		} else {
			if connecting {
				m.poolDelay = min(max(m.poolDelay, poolStartInterval)+poolRetryStep, max(poolStartInterval, sec(s.RetryBackoffMax)))
			}
			l.retryBackoff = m.fallbackDelay()
			m.nextLaunch = maxTime(m.nextLaunch, now.Add(l.retryBackoff))
		}
	} else {
		l.retryBackoff = min(max(l.retryBackoff*2, sec(s.RetryBackoff)), sec(s.RetryBackoffMax))
	}
	l.burst = false
	l.status = protocol.LaneBackoff
	l.nextStart = now.Add(l.retryBackoff)
	l.restarts++
	reason := l.lastError
	if reason == "" {
		reason = fmt.Sprintf("no handshake within %ds", s.ConnectTimeout)
	}
	msg := fmt.Sprintf("%s; retrying in %s", reason, l.retryBackoff.Round(time.Second))
	if pool && burst {
		msg = reason + "; waiting for the rest of the connection burst"
	}
	if len(l.spec.Keys) > 1 {
		l.keyIdx = (l.keyIdx + 1) % len(l.spec.Keys)
		msg += fmt.Sprintf(" with key %d", l.keyID())
	}
	l.lastError = msg
	m.event("warn", protocol.EventLaneBackoff, l.spec.ID, msg)
	// Pool fallback uses 60s, 90s, 120s, ... instead of long breaker pauses.
	if pool {
		return
	}
	m.failures++
	if s.BreakerFailures > 0 && s.BreakerPause > 0 && (m.failures >= s.BreakerFailures || m.trips > 0) {
		pause := sec(s.BreakerPause) << min(m.trips, 2)
		m.pausedUntil = now.Add(pause)
		m.failures = 0
		m.trips++
		msg := fmt.Sprintf("%d lanes failed to connect in a row; no new connections for %s", s.BreakerFailures, pause)
		if m.trips > 1 {
			msg = fmt.Sprintf("probe lane failed to connect; no new connections for %s", pause)
		}
		m.event("warn", protocol.EventBreakerOpen, "", msg)
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

// Restart queues one lane (admin action). Pool attempts keep their shared budget.
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
	l.lastError = ""
	l.restarts++
	if m.settings.TargetUp <= 0 {
		m.pausedUntil = time.Time{}
		m.nextLaunch = time.Time{}
	}
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
		if l.tun != nil || l.status == protocol.LaneBackoff {
			l.restarts++ // untried pool candidates stay on standby
		}
		m.stop(l)
		l.status = protocol.LaneQueued
		l.nextStart = time.Time{}
		l.retryBackoff = 0
		l.lastError = ""
	}
	if m.settings.TargetUp <= 0 {
		m.pausedUntil = time.Time{}
		m.trips = 0
	}
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
	paused := m.now().Before(m.pausedUntil)
	fallbackWaiting := m.settings.TargetUp > 0 && m.fallback && m.now().Before(m.nextLaunch)
	for _, id := range m.order {
		l := m.lanes[id]
		if m.settings.TargetUp > 0 && l.status == protocol.LaneQueued && l.restarts == 0 && l.lastError == "" {
			continue // untried pool candidate: control shows it as standby
		}
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
		switch {
		case l.status == protocol.LaneBackoff:
			ns := maxTime(l.nextStart, m.pausedUntil)
			if m.settings.TargetUp > 0 && m.fallback {
				ns = maxTime(ns, m.nextLaunch)
			}
			st.NextRetry = &ns
		case l.status == protocol.LaneQueued && (paused || fallbackWaiting):
			ns := m.pausedUntil
			if fallbackWaiting {
				ns = maxTime(ns, m.nextLaunch)
			}
			st.NextRetry = &ns
			if st.LastError == "" {
				st.LastError = "waiting: new connections paused by the circuit breaker"
				if fallbackWaiting {
					st.LastError = "waiting for the next pool fallback attempt"
				}
			}
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
		if l.status != protocol.LaneUp || l.tun == nil || l.checking || now.Before(l.nextIPCheck) || (s.TargetUp > 0 && l.ipChecked) {
			continue
		}
		l.checking = true
		l.ipChecked = true
		gen, tun := l.gen, l.tun
		go func() {
			ip, latency, err := checkIP(ctx, tun, s.IPCheckURL, sec(s.DialTimeout))
			m.mu.Lock()
			defer m.mu.Unlock()
			if l.gen != gen {
				return
			}
			l.checking = false
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
