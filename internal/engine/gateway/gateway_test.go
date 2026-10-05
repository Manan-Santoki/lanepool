package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/Manan-Santoki/lanepool/internal/auth"
	"github.com/Manan-Santoki/lanepool/internal/engine/lanes"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/wg/wgtest"
)

type env struct {
	t      *testing.T
	lanes  *lanes.Manager
	srv    *Server
	policy *Policy
	users  []protocol.UserSpec
	burned []protocol.BurnedIP
	set    protocol.EngineSettings
}

// newEnv starts one lane per country code, each behind its own fake provider
// whose exit IP is 203.0.113.<n>.
func newEnv(t *testing.T, countries ...string) *env {
	t.Helper()
	s := protocol.DefaultSettings()
	s.LaneStartDelay, s.MaxConnecting, s.ConnectTimeout = 0, 10, 5
	s.IPCheckURL = "http://api.ipify.org/"
	var specs []protocol.LaneSpec
	for i, cc := range countries {
		priv, pub := wgtest.KeyPair()
		p := wgtest.Start(t, fmt.Sprintf("203.0.113.%d", i+1), pub)
		specs = append(specs, protocol.LaneSpec{
			ID: fmt.Sprintf("test:%s-%d", cc, i+1), Name: fmt.Sprintf("%s-%d", cc, i+1), CountryCode: strings.ToUpper(cc),
			Enabled: true, Endpoint: p.Endpoint, PeerKey: p.PublicKey,
			Addresses: []string{wgtest.ClientAddr.String()}, DNS: []string{wgtest.DNSAddr.String()},
			Keys: []protocol.LaneKey{{ID: 1, PrivateKey: priv}},
		})
	}
	m := lanes.New(s, nil)
	m.Apply(s, specs)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx, 50*time.Millisecond); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.Now().Add(10 * time.Second)
	for {
		up := 0
		for _, l := range m.Usable() {
			if l.ExitIP != "" {
				up++
			}
		}
		if up == len(countries) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lanes not up: %+v", m.States())
		}
		time.Sleep(50 * time.Millisecond)
	}

	e := &env{t: t, lanes: m, policy: NewPolicy(), set: s}
	e.srv = &Server{Policy: e.policy, Lanes: m}
	e.srv.SetSettings(s)
	if err := e.srv.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) addUser(spec protocol.UserSpec, password string) {
	h, err := auth.HashPassword(password)
	if err != nil {
		e.t.Fatal(err)
	}
	spec.PasswordHash = h
	if spec.ID == 0 {
		spec.ID = int64(len(e.users) + 1)
	}
	e.users = append(e.users, spec)
	e.apply()
}

func (e *env) apply() {
	e.policy.Apply(e.set, e.users, e.burned)
	e.srv.SetSettings(e.set)
}

// clientTimeout bounds one request through the proxy. Under -race a user's
// first request (an argon2id check before the login is cached) plus the two
// userspace network stacks took 13 s on a laptop; 10 s made tests flaky.
const clientTimeout = 60 * time.Second

// httpGet fetches http://api.ipify.org/ through the proxy and returns the body
// (the exit IP) or the error status.
func (e *env) httpGet(user, pass string) (string, int) {
	e.t.Helper()
	pu := &url.URL{Scheme: "http", Host: e.srv.Addr(), User: url.UserPassword(user, pass)}
	c := http.Client{Timeout: clientTimeout, Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true}}
	resp, err := c.Get("http://api.ipify.org/")
	if err != nil {
		e.t.Fatalf("GET through proxy: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(b)), resp.StatusCode
}

func (e *env) socksGet(user, pass string) (string, error) {
	d, err := proxy.SOCKS5("tcp", e.srv.Addr(), &proxy.Auth{User: user, Password: pass}, proxy.Direct)
	if err != nil {
		return "", err
	}
	c := http.Client{Timeout: clientTimeout, Transport: &http.Transport{
		DialContext: func(ctx context.Context, n, a string) (net.Conn, error) { return d.Dial(n, a) }, DisableKeepAlives: true}}
	resp, err := c.Get("http://api.ipify.org/")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(b)), nil
}

func TestHTTPProxyRotatesAcrossLanes(t *testing.T) {
	e := newEnv(t, "us", "de")
	e.addUser(protocol.UserSpec{Username: "alice", Enabled: true, LogDestinations: true}, "password123")

	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		ip, code := e.httpGet("alice", "password123")
		if code != 200 {
			t.Fatalf("status %d: %s", code, ip)
		}
		seen[ip] = true
	}
	if len(seen) != 2 {
		t.Fatalf("round robin used %v", seen)
	}
	// The client can finish reading before the gateway records the connection.
	var recs []protocol.ConnRecord
	for deadline := time.Now().Add(2 * time.Second); len(recs) < 4 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		r, _ := e.srv.DrainRecords()
		recs = append(recs, r...)
	}
	if len(recs) != 4 || recs[0].BytesUp == 0 || recs[0].Result != protocol.ResultOK || recs[0].Target != "api.ipify.org:80" || recs[0].ExitIP == "" || recs[0].BytesDown == 0 {
		t.Fatalf("records %+v", recs)
	}
	var down int64
	for _, u := range e.policy.DrainUsage() {
		down += u.BytesDown
	}
	if down == 0 {
		t.Fatal("no usage recorded")
	}
}

func TestSOCKS5AndUsernameParams(t *testing.T) {
	e := newEnv(t, "us", "de")
	e.addUser(protocol.UserSpec{Username: "bob", Enabled: true}, "password123")

	for i := 0; i < 3; i++ {
		ip, err := e.socksGet("bob-country-de", "password123")
		if err != nil || ip != "203.0.113.2" {
			t.Fatalf("country=de gave %q %v", ip, err)
		}
	}
	first, err := e.socksGet("bob-session-abc", "password123")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if ip, _ := e.socksGet("bob-session-abc", "password123"); ip != first {
			t.Fatalf("sticky session moved from %s to %s", first, ip)
		}
	}
	if _, err := e.socksGet("bob", "wrong-password"); err == nil {
		t.Fatal("SOCKS5 accepted a wrong password")
	}
	recs, _ := e.srv.DrainRecords()
	last := recs[len(recs)-1]
	if last.Result != protocol.ResultAuthFailed || last.Target != "" {
		t.Fatalf("auth failure record %+v (destinations must not be logged by default)", last)
	}
}

func TestCONNECTTunnelAndKick(t *testing.T) {
	e := newEnv(t, "us")
	e.addUser(protocol.UserSpec{Username: "carol", Enabled: true}, "password123")

	c, err := net.Dial("tcp", e.srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT 198.51.100.7:7 HTTP/1.1\r\nHost: 198.51.100.7:7\r\nProxy-Authorization: Basic %s\r\n\r\n", basic("carol", "password123"))
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("CONNECT: %v %v", resp, err)
	}
	fmt.Fprint(c, "hello")
	buf := make([]byte, 5)
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("echo %q %v", buf, err)
	}
	live := e.srv.Live()
	if len(live) != 1 || live[0].Username != "carol" || live[0].BytesUp != 5 {
		t.Fatalf("live %+v", live)
	}
	if n := e.srv.Kick(protocol.KickRequest{UserID: live[0].UserID}); n != 1 {
		t.Fatalf("kicked %d", n)
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := br.ReadByte(); err == nil {
		t.Fatal("connection still open after kick")
	}
	time.Sleep(100 * time.Millisecond)
	recs, _ := e.srv.DrainRecords()
	if len(recs) != 1 || recs[0].Result != protocol.ResultKicked || recs[0].Protocol != "connect" {
		t.Fatalf("records %+v", recs)
	}
}

func basic(u, p string) string {
	r, _ := http.NewRequest("GET", "/", nil)
	r.SetBasicAuth(u, p)
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")
}

func TestRejections(t *testing.T) {
	e := newEnv(t, "us")
	past := time.Now().Add(-time.Hour)
	e.addUser(protocol.UserSpec{Username: "ok", Enabled: true}, "password123")
	e.addUser(protocol.UserSpec{Username: "off", Enabled: false}, "password123")
	e.addUser(protocol.UserSpec{Username: "expired", Enabled: true, ExpiresAt: &past}, "password123")
	e.addUser(protocol.UserSpec{Username: "quota", Enabled: true, QuotaBytes: 100, UsedBytes: 100}, "password123")
	e.addUser(protocol.UserSpec{Username: "nodomain", Enabled: true, DenyDomains: []string{"ipify.org"}}, "password123")
	e.addUser(protocol.UserSpec{Username: "onlyde", Enabled: true, AllowedCountries: []string{"de"}}, "password123")
	e.addUser(protocol.UserSpec{Username: "office", Enabled: true, AllowedCIDRs: []string{"10.9.9.0/24"}}, "password123")

	cases := []struct {
		user, pass string
		want       int
	}{
		{"ok", "nope-wrong", http.StatusProxyAuthRequired},
		{"nobody", "password123", http.StatusProxyAuthRequired},
		{"off", "password123", http.StatusForbidden},
		{"expired", "password123", http.StatusForbidden},
		{"quota", "password123", http.StatusPaymentRequired},
		{"nodomain", "password123", http.StatusForbidden},
		{"onlyde", "password123", http.StatusServiceUnavailable}, // no German lane
		{"office", "password123", http.StatusForbidden},          // wrong client IP
		{"ok", "password123", http.StatusOK},
	}
	for _, c := range cases {
		if _, code := e.httpGet(c.user, c.pass); code != c.want {
			t.Errorf("%s: status %d, want %d", c.user, code, c.want)
		}
	}

	e.set.Paused = true
	e.apply()
	if _, code := e.httpGet("ok", "password123"); code != http.StatusServiceUnavailable {
		t.Errorf("paused: status %d", code)
	}
}

func TestConnectionLimits(t *testing.T) {
	e := newEnv(t, "us")
	e.addUser(protocol.UserSpec{Username: "one", Enabled: true, MaxConnections: 1}, "password123")

	c, _ := net.Dial("tcp", e.srv.Addr())
	defer c.Close()
	fmt.Fprintf(c, "CONNECT 198.51.100.7:7 HTTP/1.1\r\nProxy-Authorization: Basic %s\r\n\r\n", basic("one", "password123"))
	if resp, err := http.ReadResponse(bufio.NewReader(c), nil); err != nil || resp.StatusCode != 200 {
		t.Fatalf("first connection: %v %v", resp, err)
	}
	if _, code := e.httpGet("one", "password123"); code != http.StatusTooManyRequests {
		t.Fatalf("second connection status %d, want 429", code)
	}
}

func TestBurnedLaneIsAvoided(t *testing.T) {
	e := newEnv(t, "us", "de")
	e.addUser(protocol.UserSpec{Username: "dave", Enabled: true}, "password123")
	e.burned = []protocol.BurnedIP{{Domain: "ipify.org", LaneID: "test:us-1", ExpiresAt: time.Now().Add(time.Hour)}}
	e.apply()
	for i := 0; i < 4; i++ {
		if ip, _ := e.httpGet("dave", "password123"); ip != "203.0.113.2" {
			t.Fatalf("burned lane used: %s", ip)
		}
	}
}

func TestParseUsername(t *testing.T) {
	cases := map[string]Params{
		"alice":                            {Username: "alice"},
		"my-user":                          {Username: "my-user"},
		"alice-country-US":                 {Username: "alice", Country: "us"},
		"alice-session-abc-sessttl-30":     {Username: "alice", Session: "abc", SessionTTL: 30 * time.Minute},
		"my-user-lane-us-nyc":              {Username: "my-user", Lane: "us"}, // lane names with hyphens need the ID form
		"bob-country-de-session-x1-lane-y": {Username: "bob", Country: "de", Session: "x1", Lane: "y"},
	}
	for in, want := range cases {
		if got := ParseUsername(in); got != want {
			t.Errorf("%s: got %+v want %+v", in, got, want)
		}
	}
}

func TestDomainMatch(t *testing.T) {
	for _, c := range []struct {
		host, pattern string
		want          bool
	}{
		{"api.ipify.org", "ipify.org", true}, {"ipify.org", "ipify.org", true}, {"notipify.org", "ipify.org", false},
		{"a.b.com", "*.b.com", true}, {"x.com", "*", true}, {"x.com", "", false},
	} {
		if got := domainMatch(c.host, c.pattern); got != c.want {
			t.Errorf("%s ~ %s = %v", c.host, c.pattern, got)
		}
	}
}
