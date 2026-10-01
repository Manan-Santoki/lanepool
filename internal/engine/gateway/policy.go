package gateway

import (
	"crypto/sha256"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/Manan-Santoki/lanepool/internal/auth"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

// Params are options passed in the proxy username, the way commercial proxy
// providers do it: "alice-country-us-session-abc123".
type Params struct {
	Username   string // base username
	Country    string // lowercase ISO code
	Lane       string // lane ID or name
	Session    string // sticky session ID
	SessionTTL time.Duration
}

var paramKeys = map[string]bool{"country": true, "lane": true, "session": true, "sessttl": true}

// ParseUsername splits "alice-country-us-session-abc" into the base username and
// parameters. Everything before the first known "-key-" is the base username, so
// plain usernames may contain hyphens.
func ParseUsername(s string) Params {
	parts := strings.Split(s, "-")
	first := len(parts)
	for i := 1; i+1 < len(parts); i++ {
		if paramKeys[strings.ToLower(parts[i])] {
			first = i
			break
		}
	}
	p := Params{Username: strings.Join(parts[:first], "-")}
	for i := first; i+1 < len(parts); i += 2 {
		v := parts[i+1]
		switch strings.ToLower(parts[i]) {
		case "country":
			p.Country = strings.ToLower(v)
		case "lane":
			p.Lane = v
		case "session":
			p.Session = v
		case "sessttl":
			if d, err := time.ParseDuration(v + "m"); err == nil && d > 0 {
				p.SessionTTL = d
			}
		}
	}
	return p
}

// user is a UserSpec with its rules compiled.
type user struct {
	spec      protocol.UserSpec
	countries map[string]bool
	lanes     map[string]bool
	cidrs     []netip.Prefix
	limiter   *rate.Limiter
}

func compileUser(spec protocol.UserSpec) *user {
	u := &user{spec: spec, countries: set(spec.AllowedCountries), lanes: set(spec.AllowedLanes)}
	for _, c := range spec.AllowedCIDRs {
		if p, err := netip.ParsePrefix(strings.TrimSpace(c)); err == nil {
			u.cidrs = append(u.cidrs, p)
		} else if a, err := netip.ParseAddr(strings.TrimSpace(c)); err == nil {
			u.cidrs = append(u.cidrs, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	if spec.ConnPerSecond > 0 {
		burst := max(1, int(spec.ConnPerSecond))
		u.limiter = rate.NewLimiter(rate.Limit(spec.ConnPerSecond), burst)
	}
	return u
}

func set(list []string) map[string]bool {
	m := map[string]bool{}
	for _, v := range list {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			m[v] = true
		}
	}
	return m
}

func (u *user) clientAllowed(ip netip.Addr) bool {
	if len(u.cidrs) == 0 {
		return true
	}
	for _, p := range u.cidrs {
		if p.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

func (u *user) targetAllowed(host string) bool {
	for _, d := range u.spec.DenyDomains {
		if domainMatch(host, d) {
			return false
		}
	}
	if len(u.spec.AllowDomains) == 0 {
		return true
	}
	for _, d := range u.spec.AllowDomains {
		if domainMatch(host, d) {
			return true
		}
	}
	return false
}

// domainMatch reports whether host is pattern or a subdomain of it. "*" matches
// everything; IP literals only match exactly.
func domainMatch(host, pattern string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	pattern = strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(pattern)), "."), "*.")
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	return host == pattern || strings.HasSuffix(host, "."+pattern)
}

// Policy holds users, limits, usage counters, sticky sessions and burned IPs.
// It is shared by all connections.
type Policy struct {
	mu        sync.Mutex
	users     map[string]*user // by username
	authCache map[[32]byte]time.Time
	active    map[int64]int // open connections per user
	pending   map[int64]int64
	usage     map[usageKey]*protocol.UsageDelta
	sticky    map[string]stickyLane
	burned    []protocol.BurnedIP
	autoBurn  []protocol.BurnedIP
	failures  map[string]int // lane|domain -> consecutive dial failures
	settings  protocol.EngineSettings
	authFails map[netip.Addr]*failWindow
}

type usageKey struct {
	user int64
	lane string
}

type stickyLane struct {
	lane    string
	expires time.Time
}

type failWindow struct {
	count int
	reset time.Time
}

// NewPolicy creates an empty policy.
func NewPolicy() *Policy {
	return &Policy{
		users: map[string]*user{}, authCache: map[[32]byte]time.Time{}, active: map[int64]int{},
		pending: map[int64]int64{}, usage: map[usageKey]*protocol.UsageDelta{}, sticky: map[string]stickyLane{},
		failures: map[string]int{}, authFails: map[netip.Addr]*failWindow{}, settings: protocol.DefaultSettings(),
	}
}

// Apply loads a new configuration. Limiters, counters and sticky sessions of
// users that still exist are kept.
func (p *Policy) Apply(settings protocol.EngineSettings, users []protocol.UserSpec, burned []protocol.BurnedIP) {
	p.mu.Lock()
	defer p.mu.Unlock()
	next := make(map[string]*user, len(users))
	for _, spec := range users {
		u := compileUser(spec)
		if old, ok := p.users[strings.ToLower(spec.Username)]; ok && old.spec.ConnPerSecond == spec.ConnPerSecond {
			u.limiter = old.limiter
		}
		if old, ok := p.users[strings.ToLower(spec.Username)]; ok && old.spec.PasswordHash != spec.PasswordHash {
			p.authCache = map[[32]byte]time.Time{} // password changed: forget cached logins
		}
		next[strings.ToLower(spec.Username)] = u
	}
	// UsedBytes from control includes everything already reported; only traffic
	// that hasn't been drained into a report yet is still pending locally.
	p.pending = map[int64]int64{}
	for k, d := range p.usage {
		p.pending[k.user] += d.BytesUp + d.BytesDown
	}
	p.users = next
	p.burned = burned
	p.settings = settings
}

func credKey(username, password string) [32]byte {
	return sha256.Sum256([]byte(username + "\x00" + password))
}

// Authenticate checks credentials and the user's account rules. It returns the
// user, or a result code explaining the rejection.
func (p *Policy) Authenticate(username, password string, client netip.Addr) (*user, Params, string) {
	params := ParseUsername(username)
	now := time.Now()

	p.mu.Lock()
	if w := p.authFails[client]; w != nil && now.Before(w.reset) && w.count >= 10 {
		p.mu.Unlock()
		return nil, params, protocol.ResultAuthFailed // too many failures from this IP
	}
	u := p.users[strings.ToLower(params.Username)]
	cached := false
	if u != nil {
		exp, ok := p.authCache[credKey(params.Username, password)]
		cached = ok && now.Before(exp)
	}
	p.mu.Unlock()

	ok := cached
	if u != nil && !cached {
		ok = auth.VerifyPassword(u.spec.PasswordHash, password)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if !ok {
		w := p.authFails[client]
		if w == nil || now.After(w.reset) {
			w = &failWindow{reset: now.Add(5 * time.Minute)}
			p.authFails[client] = w
		}
		w.count++
		return nil, params, protocol.ResultAuthFailed
	}
	if !cached {
		p.authCache[credKey(params.Username, password)] = now.Add(5 * time.Minute)
	}
	switch {
	case !u.spec.Enabled:
		return u, params, protocol.ResultDenied
	case u.spec.ExpiresAt != nil && now.After(*u.spec.ExpiresAt):
		return u, params, protocol.ResultDenied
	case !u.clientAllowed(client):
		return u, params, protocol.ResultDenied
	case u.spec.QuotaBytes > 0 && u.spec.UsedBytes+p.pending[u.spec.ID] >= u.spec.QuotaBytes:
		return u, params, protocol.ResultQuota
	}
	return u, params, ""
}

// Admit applies the per-user connection limits and reserves a slot. The
// returned release function frees it.
func (p *Policy) Admit(u *user) (func(), string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if u.spec.MaxConnections > 0 && p.active[u.spec.ID] >= u.spec.MaxConnections {
		return nil, protocol.ResultRateLimited
	}
	if u.limiter != nil && !u.limiter.Allow() {
		return nil, protocol.ResultRateLimited
	}
	p.active[u.spec.ID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			p.active[u.spec.ID]--
			p.mu.Unlock()
		})
	}, ""
}

// AddUsage records traffic for quotas and reporting.
func (p *Policy) AddUsage(userID int64, lane string, up, down int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.delta(userID, lane)
	d.BytesUp += up
	d.BytesDown += down
	p.pending[userID] += up + down
}

// CountConnection records a finished connection for usage reporting.
func (p *Policy) CountConnection(userID int64, lane string, failed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.delta(userID, lane)
	d.Connections++
	if failed {
		d.Failures++
	}
}

func (p *Policy) delta(userID int64, lane string) *protocol.UsageDelta {
	k := usageKey{userID, lane}
	d := p.usage[k]
	if d == nil {
		d = &protocol.UsageDelta{UserID: userID, LaneID: lane}
		p.usage[k] = d
	}
	return d
}

// DrainUsage returns traffic since the last call.
func (p *Policy) DrainUsage() []protocol.UsageDelta {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]protocol.UsageDelta, 0, len(p.usage))
	for _, d := range p.usage {
		out = append(out, *d)
	}
	p.usage = map[usageKey]*protocol.UsageDelta{}
	return out
}

// Burned reports whether lane is marked as blocked by host.
func (p *Policy) Burned(lane, host string) bool {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, list := range [][]protocol.BurnedIP{p.burned, p.autoBurn} {
		for _, b := range list {
			if b.LaneID == lane && now.Before(b.ExpiresAt) && domainMatch(host, b.Domain) {
				return true
			}
		}
	}
	return false
}

// DialResult feeds automatic burned-IP detection: after AutoBurnFailures dial
// failures in a row from one lane to one host, that lane is avoided for the host.
func (p *Policy) DialResult(lane, host string, ok bool) (burned *protocol.BurnedIP) {
	host = strings.ToLower(host)
	if _, err := netip.ParseAddr(host); err == nil {
		return nil // only domains are tracked
	}
	k := lane + "|" + host
	p.mu.Lock()
	defer p.mu.Unlock()
	if ok {
		delete(p.failures, k)
		return nil
	}
	p.failures[k]++
	if p.settings.AutoBurnFailures <= 0 || p.failures[k] < p.settings.AutoBurnFailures {
		return nil
	}
	delete(p.failures, k)
	b := protocol.BurnedIP{Domain: host, LaneID: lane, ExpiresAt: time.Now().Add(time.Duration(p.settings.AutoBurnTTL) * time.Second)}
	p.autoBurn = append(p.autoBurn, b)
	return &b
}

// stickyKey identifies a sticky session: the session parameter if given,
// otherwise the client IP for users with StickyMinutes set.
func stickyKey(u *user, params Params, client netip.Addr) (string, time.Duration) {
	ttl := time.Duration(u.spec.StickyMinutes) * time.Minute
	if params.SessionTTL > 0 {
		ttl = params.SessionTTL
	}
	switch {
	case params.Session != "":
		if ttl == 0 {
			ttl = 10 * time.Minute
		}
		return u.spec.Username + "|s|" + params.Session, ttl
	case ttl > 0:
		return u.spec.Username + "|ip|" + client.String(), ttl
	}
	return "", 0
}

func (p *Policy) stickyGet(key string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sticky[key]
	if !ok || time.Now().After(s.expires) {
		delete(p.sticky, key)
		return ""
	}
	return s.lane
}

func (p *Policy) stickySet(key, lane string, ttl time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sticky[key] = stickyLane{lane: lane, expires: time.Now().Add(ttl)}
	if len(p.sticky) > 100000 { // drop expired entries now and then
		now := time.Now()
		for k, v := range p.sticky {
			if now.After(v.expires) {
				delete(p.sticky, k)
			}
		}
	}
}

// ForgetSticky drops a session mapping so the next connection gets a fresh lane.
func (p *Policy) ForgetSticky(username, session string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sticky, username+"|s|"+session)
}

func clientAddr(a net.Addr) netip.Addr {
	if tcp, ok := a.(*net.TCPAddr); ok {
		if ip, ok := netip.AddrFromSlice(tcp.IP); ok {
			return ip.Unmap()
		}
	}
	if ap, err := netip.ParseAddrPort(a.String()); err == nil {
		return ap.Addr().Unmap()
	}
	return netip.Addr{}
}
