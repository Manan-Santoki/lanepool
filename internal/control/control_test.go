package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Manan-Santoki/lanepool/internal/engine"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/providers/surfshark"
	"github.com/Manan-Santoki/lanepool/internal/wg/wgtest"
)

func testDatabaseURL(t *testing.T) string {
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		u = "postgres://localhost:5432/lanepool_test?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, u)
	if err != nil {
		t.Skipf("no test database (%v); set TEST_DATABASE_URL", err)
	}
	defer conn.Close(ctx)
	// Start from an empty schema.
	if _, err := conn.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	return u
}

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// stack is control + engine wired together.
type stack struct {
	t         *testing.T
	srv       *Server
	api       *httptest.Server
	proxyAddr string
	client    *http.Client // cookie jar; acts like the dashboard
}

func newStack(t *testing.T, surfsharkAPI string) *stack {
	dbURL := testDatabaseURL(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	engineAPI, proxyAddr := freeAddr(t), freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv, err := New(ctx, Config{
		DatabaseURL: dbURL, Secret: "test-secret", EngineURL: "http://" + engineAPI, EngineToken: "engine-token",
		SurfsharkAPI: surfsharkAPI,
	}, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	api := httptest.NewServer(srv.Router())
	t.Cleanup(api.Close)
	go srv.jobs(ctx)
	go srv.laneSyncLoop(ctx)

	eng := engine.New(engine.Config{
		ControlURL: api.URL, Token: "engine-token", ProxyAddr: proxyAddr, APIAddr: engineAPI,
		PollInterval: 300 * time.Millisecond, ReportInterval: 200 * time.Millisecond,
	}, log)
	done := make(chan struct{})
	go func() { eng.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	jar, _ := cookiejar.New(nil)
	return &stack{t: t, srv: srv, api: api, proxyAddr: proxyAddr, client: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
}

// call sends a JSON request and decodes the response into out (if not nil).
func (s *stack) call(c *http.Client, method, path string, body, out any, hdr ...string) int {
	s.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, s.api.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			s.t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode
}

func (s *stack) waitFor(what string, timeout time.Duration, cond func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.t.Fatalf("timed out waiting for %s", what)
}

func wgQuick(p *wgtest.Provider, priv string) string {
	return fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = %s/32\nDNS = %s\nPostUp = echo ignored\n\n[Peer]\nPublicKey = %s\nAllowedIPs = 0.0.0.0/0\nEndpoint = %s\n",
		priv, wgtest.ClientAddr, wgtest.DNSAddr, p.PublicKey, p.Endpoint)
}

func TestFullStack(t *testing.T) {
	noServers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("[]")) }))
	defer noServers.Close()
	st := newStack(t, noServers.URL)

	// --- setup and auth ---
	var setup struct{ NeedsSetup bool }
	st.call(st.client, "GET", "/api/setup", nil, &setup)
	if !setup.NeedsSetup {
		t.Fatal("fresh database should need setup")
	}
	if code := st.call(st.client, "GET", "/api/overview", nil, nil); code != 401 {
		t.Fatalf("overview before login: %d", code)
	}
	if code := st.call(st.client, "POST", "/api/setup", map[string]string{"email": "Owner@Example.com", "name": "Owner", "password": "short"}, nil); code != 422 {
		t.Fatalf("weak password accepted: %d", code)
	}
	if code := st.call(st.client, "POST", "/api/setup", map[string]string{"email": "owner@example.com", "name": "Owner", "password": "a-long-password"}, nil); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	if code := st.call(st.client, "POST", "/api/setup", map[string]string{"email": "x@example.com", "password": "a-long-password"}, nil); code != 409 {
		t.Fatalf("second setup: %d", code)
	}
	var me struct{ Admin Admin }
	st.call(st.client, "GET", "/api/auth/me", nil, &me)
	if me.Admin.Email != "owner@example.com" || me.Admin.Role != "admin" {
		t.Fatalf("me %+v", me)
	}
	// Cross-site mutation is blocked.
	if code := st.call(st.client, "POST", "/api/users", map[string]string{"username": "evil"}, nil, "Origin", "https://evil.example"); code != 403 {
		t.Fatalf("cross-site POST: %d", code)
	}

	// --- a proxy user and a lane ---
	var created struct {
		User     ProxyUser
		Password string
	}
	if code := st.call(st.client, "POST", "/api/users", map[string]any{"username": "alice-session-x"}, nil); code != 422 {
		t.Fatalf("username with parameter keyword accepted: %d", code)
	}
	if code := st.call(st.client, "POST", "/api/users", map[string]any{"username": "alice", "logDestinations": true, "quotaBytes": 1 << 30}, &created); code != 200 || created.Password == "" {
		t.Fatalf("create user: %d %+v", code, created)
	}

	priv, pub := wgtest.KeyPair()
	prov := wgtest.Start(t, "203.0.113.77", pub)
	var wgc wireguardConfig
	if code := st.call(st.client, "POST", "/api/providers/wireguard", map[string]any{"name": "test-exit", "config": wgQuick(prov, priv), "countryCode": "nl", "city": "Amsterdam"}, &wgc); code != 200 {
		t.Fatalf("add wireguard config: %d", code)
	}
	var lanes []Lane
	st.waitFor("lane up", 15*time.Second, func() bool {
		st.call(st.client, "GET", "/api/lanes", nil, &lanes)
		return len(lanes) == 1 && lanes[0].Status == "up" && lanes[0].ExitIP == "203.0.113.77"
	})
	if lanes[0].CountryCode != "NL" || lanes[0].Provider != "wireguard" {
		t.Fatalf("lane %+v", lanes[0])
	}

	// --- traffic through the proxy ---
	pu := &url.URL{Scheme: "http", Host: st.proxyAddr, User: url.UserPassword("alice", created.Password)}
	hc := http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true}}
	var resp *http.Response
	st.waitFor("proxy accepts alice", 10*time.Second, func() bool {
		r, err := hc.Get("http://api.ipify.org/")
		if err != nil || r.StatusCode != 200 {
			if r != nil {
				r.Body.Close()
			}
			return false
		}
		resp = r
		return true
	})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(body)) != "203.0.113.77" {
		t.Fatalf("exit IP %q", body)
	}

	var logs struct {
		Items []ConnLog
	}
	st.waitFor("connection log", 10*time.Second, func() bool {
		st.call(st.client, "GET", "/api/logs/connections?user=alice", nil, &logs)
		return len(logs.Items) > 0
	})
	l := logs.Items[0]
	if l.Username != "alice" || l.Target != "api.ipify.org:80" || l.ExitIP != "203.0.113.77" || l.Result != "ok" || l.LaneID != lanes[0].ID {
		t.Fatalf("log %+v", l)
	}
	var u ProxyUser
	st.waitFor("usage", 5*time.Second, func() bool {
		st.call(st.client, "GET", fmt.Sprintf("/api/users/%d", created.User.ID), nil, &u)
		return u.UsedBytes > 0 && u.LastSeenAt != nil
	})
	var points []trafficPoint
	st.call(st.client, "GET", "/api/analytics/traffic?range=24h", nil, &points)
	var down int64
	for _, p := range points {
		down += p.BytesDown
	}
	if len(points) != 24 || down == 0 {
		t.Fatalf("traffic: %d points, %d bytes down", len(points), down)
	}
	var top []topItem
	st.call(st.client, "GET", "/api/analytics/top?by=domain", nil, &top)
	if len(top) == 0 || top[0].Key != "api.ipify.org" {
		t.Fatalf("top domains %+v", top)
	}
	csvReq, _ := http.NewRequest("GET", st.api.URL+"/api/logs/connections.csv", nil)
	csvResp, err := st.client.Do(csvReq)
	if err != nil {
		t.Fatal(err)
	}
	csvBody, _ := io.ReadAll(csvResp.Body)
	csvResp.Body.Close()
	if !strings.Contains(string(csvBody), "api.ipify.org:80") {
		t.Fatalf("csv %q", csvBody)
	}

	// --- live connections and kick ---
	c, err := net.Dial("tcp", st.proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	auth := basicAuth("alice", created.Password)
	fmt.Fprintf(c, "CONNECT 198.51.100.7:7 HTTP/1.1\r\nProxy-Authorization: Basic %s\r\n\r\n", auth)
	buf := make([]byte, 64)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _ := c.Read(buf)
	if !strings.Contains(string(buf[:n]), "200") {
		t.Fatalf("CONNECT: %q", buf[:n])
	}
	var live []map[string]any
	st.waitFor("live connection", 5*time.Second, func() bool {
		st.call(st.client, "GET", "/api/connections", nil, &live)
		return len(live) == 1
	})
	var kicked struct{ Kicked int }
	st.call(st.client, "POST", "/api/connections/kick", map[string]any{"userId": created.User.ID}, &kicked)
	if kicked.Kicked != 1 {
		t.Fatalf("kicked %d", kicked.Kicked)
	}

	// --- overview and events ---
	var ov map[string]any
	st.call(st.client, "GET", "/api/overview", nil, &ov)
	if ov["uniqueExitIps"].(float64) != 1 || !ov["engine"].(map[string]any)["connected"].(bool) {
		t.Fatalf("overview %v", ov)
	}
	var events struct{ Items []Event }
	st.call(st.client, "GET", "/api/events?type=admin.", nil, &events)
	if len(events.Items) < 3 {
		t.Fatalf("audit events %+v", events.Items)
	}

	// --- roles and tokens ---
	if code := st.call(st.client, "POST", "/api/admins", map[string]string{"email": "viewer@example.com", "password": "viewer-password", "role": "viewer"}, nil); code != 200 {
		t.Fatalf("create viewer: %d", code)
	}
	jar, _ := cookiejar.New(nil)
	viewer := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	if code := st.call(viewer, "POST", "/api/auth/login", map[string]string{"email": "viewer@example.com", "password": "wrong-password"}, nil); code != 401 {
		t.Fatalf("wrong password login: %d", code)
	}
	if code := st.call(viewer, "POST", "/api/auth/login", map[string]string{"email": "VIEWER@example.com", "password": "viewer-password"}, nil); code != 200 {
		t.Fatalf("viewer login: %d", code)
	}
	if code := st.call(viewer, "GET", "/api/users", nil, nil); code != 200 {
		t.Fatalf("viewer GET: %d", code)
	}
	if code := st.call(viewer, "DELETE", fmt.Sprintf("/api/users/%d", created.User.ID), nil, nil); code != 403 {
		t.Fatalf("viewer DELETE: %d", code)
	}

	var tok struct{ Token string }
	st.call(st.client, "POST", "/api/tokens", map[string]any{"name": "app", "scopes": []string{"rotate"}}, &tok)
	plain := &http.Client{Timeout: 10 * time.Second}
	bearer := []string{"Authorization", "Bearer " + tok.Token}
	if code := st.call(plain, "GET", "/api/lanes/random", nil, nil, bearer...); code != 200 {
		t.Fatalf("token random lane: %d", code)
	}
	if code := st.call(plain, "GET", "/api/users", nil, nil, bearer...); code != 403 {
		t.Fatalf("rotate-scope token read users: %d", code)
	}
	var burned BurnedIP
	if code := st.call(plain, "POST", "/api/v1/burn", map[string]any{"domain": "example.com", "exitIp": "203.0.113.77", "ttlMinutes": 30}, &burned, bearer...); code != 200 || burned.LaneID != lanes[0].ID {
		t.Fatalf("burn via API: %d %+v", code, burned)
	}
	cfg, err := st.srv.buildEngineConfig(context.Background())
	if err != nil || len(cfg.Burned) != 1 || cfg.Burned[0].Domain != "example.com" {
		t.Fatalf("engine config burned %+v %v", cfg.Burned, err)
	}

	// --- settings validation ---
	var verr struct{ Fields map[string]string }
	if code := st.call(st.client, "PUT", "/api/settings", map[string]any{"engine": map[string]any{"maxConnecting": 0}}, &verr); code != 422 || verr.Fields["engine.maxConnecting"] == "" {
		t.Fatalf("invalid settings: %d %+v", code, verr)
	}
	if code := st.call(st.client, "PUT", "/api/settings", map[string]any{"app": map[string]any{"publicProxyHost": "proxy.example.com"}}, nil); code != 200 {
		t.Fatalf("valid settings: %d", code)
	}

	// --- disabling the lane stops it ---
	if code := st.call(st.client, "PATCH", "/api/lanes/"+url.PathEscape(lanes[0].ID), map[string]bool{"enabled": false}, nil); code != 200 {
		t.Fatalf("disable lane: %d", code)
	}
	st.waitFor("lane disabled", 5*time.Second, func() bool {
		st.call(st.client, "GET", "/api/lanes", nil, &lanes)
		return lanes[0].Status == "disabled"
	})
}

func basicAuth(u, p string) string {
	r, _ := http.NewRequest("GET", "/", nil)
	r.SetBasicAuth(u, p)
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")
}

func TestSurfsharkSelectionAndKeys(t *testing.T) {
	servers := []surfshark.Server{
		{Country: "US", CountryCode: "US", Location: "New York", ConnectionName: "us-nyc.prod.surfshark.com", PubKey: "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="},
		{Country: "DE", CountryCode: "DE", Location: "Berlin", ConnectionName: "de-ber.prod.surfshark.com", PubKey: "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="},
		{Country: "DE", CountryCode: "DE", Location: "Frankfurt", ConnectionName: "de-fra.prod.surfshark.com", PubKey: "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="},
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(servers) }))
	defer fake.Close()
	st := newStack(t, fake.URL)
	st.call(st.client, "POST", "/api/setup", map[string]string{"email": "o@example.com", "password": "a-long-password"}, nil)

	if code := st.call(st.client, "POST", "/api/providers/surfshark/keys", map[string]string{"privateKey": "not-a-key"}, nil); code != 422 {
		t.Fatalf("invalid key accepted: %d", code)
	}
	k1, _ := wgtest.KeyPair()
	k2, _ := wgtest.KeyPair()
	var key surfsharkKey
	if code := st.call(st.client, "POST", "/api/providers/surfshark/keys", map[string]string{"privateKey": k1, "label": "phone"}, &key); code != 200 || key.PublicKey == "" {
		t.Fatalf("add key: %d", code)
	}
	if code := st.call(st.client, "POST", "/api/providers/surfshark/keys", map[string]string{"privateKey": k1}, nil); code != 422 {
		t.Fatalf("duplicate key: %d", code)
	}
	st.call(st.client, "POST", "/api/providers/surfshark/keys", map[string]string{"privateKey": k2}, nil)

	var prov map[string]any
	if code := st.call(st.client, "PUT", "/api/providers/surfshark/selection", map[string]any{"lanes": 2, "countries": []string{"DE"}, "includeVirtual": true}, &prov); code != 200 {
		t.Fatalf("selection: %d", code)
	}
	var lanes []Lane
	st.call(st.client, "GET", "/api/lanes", nil, &lanes)
	if len(lanes) != 2 || lanes[0].ID != "surfshark:de-ber" || lanes[1].ID != "surfshark:de-fra" {
		t.Fatalf("lanes %+v", lanes)
	}
	cfg, err := st.srv.buildEngineConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Two keys spread across lanes: lane 0 starts with key A, lane 1 with key B.
	if len(cfg.Lanes) != 2 || len(cfg.Lanes[0].Keys) != 2 || cfg.Lanes[0].Keys[0].ID == cfg.Lanes[1].Keys[0].ID {
		t.Fatalf("keys not spread: %+v", cfg.Lanes)
	}
	if cfg.Lanes[0].Keys[0].PrivateKey != k1 || cfg.Lanes[0].Endpoint != "de-ber.prod.surfshark.com:51820" {
		t.Fatalf("lane spec %+v", cfg.Lanes[0])
	}
	// Keys are encrypted at rest.
	var enc []byte
	st.srv.db.QueryRow(context.Background(), `SELECT private_key_enc FROM provider_keys LIMIT 1`).Scan(&enc)
	if bytes.Contains(enc, []byte(k1[:20])) {
		t.Fatal("private key stored in plain text")
	}
	var locs []surfsharkLocation
	st.call(st.client, "GET", "/api/providers/surfshark/locations", nil, &locs)
	if len(locs) != 3 {
		t.Fatalf("locations %d", len(locs))
	}

	// Add a lane in another country: existing lanes stay, the count grows.
	if code := st.call(st.client, "POST", "/api/lanes/add", map[string]any{"locations": []string{"nope"}}, nil); code != 422 {
		t.Fatalf("unknown location: %d", code)
	}
	if code := st.call(st.client, "POST", "/api/lanes/add", map[string]any{"locations": []string{"us-nyc"}}, &lanes); code != 200 {
		t.Fatalf("add lane: %d", code)
	}
	if ids := laneIDs(lanes); ids != "surfshark:us-nyc,surfshark:de-ber,surfshark:de-fra" {
		t.Fatalf("after add: %s", ids)
	}
	// Remove an automatically picked lane: it isn't replaced or picked again.
	if code := st.call(st.client, "DELETE", "/api/lanes/"+url.PathEscape("surfshark:de-ber"), nil, nil); code != 204 {
		t.Fatalf("remove lane: %d", code)
	}
	st.call(st.client, "GET", "/api/lanes", nil, &lanes)
	if ids := laneIDs(lanes); ids != "surfshark:us-nyc,surfshark:de-fra" {
		t.Fatalf("after remove: %s", ids)
	}
	var sel SurfsharkSelection
	raw := map[string]json.RawMessage{}
	st.call(st.client, "GET", "/api/providers/surfshark", nil, &raw)
	json.Unmarshal(raw["selection"], &sel)
	if sel.Lanes != 2 || strings.Join(sel.Locations, ",") != "us-nyc" || strings.Join(sel.ExcludeLocations, ",") != "de-ber" {
		t.Fatalf("selection %+v", sel)
	}
	// Adding it back un-excludes it.
	st.call(st.client, "POST", "/api/lanes/add", map[string]any{"locations": []string{"de-ber"}}, &lanes)
	if len(lanes) != 3 {
		t.Fatalf("re-add: %s", laneIDs(lanes))
	}
}

func laneIDs(lanes []Lane) string {
	var ids []string
	for _, l := range lanes {
		ids = append(ids, l.ID)
	}
	return strings.Join(ids, ",")
}

func TestParseWGQuick(t *testing.T) {
	priv, pub := wgtest.KeyPair()
	p, err := parseWGQuick(fmt.Sprintf("[Interface]\n# comment\nPrivateKey = %s\nAddress = 10.0.0.2/16, fd00::2/64\nDNS = 1.1.1.1, example.com\nMTU = 1380\nPostUp = iptables -A x\n\n[Peer]\nPublicKey=%s\nEndpoint = vpn.example.com:51820\n", priv, pub))
	if err != nil {
		t.Fatal(err)
	}
	if p.PrivateKey != priv || p.PeerKey != pub || len(p.Addresses) != 2 || p.Addresses[0] != "10.0.0.2" || len(p.DNS) != 1 || p.MTU != 1380 || p.Endpoint != "vpn.example.com:51820" {
		t.Fatalf("parsed %+v", p)
	}
	if _, err := parseWGQuick("[Interface]\nPrivateKey = x\n"); err == nil {
		t.Fatal("accepted a broken config")
	}
}

func TestSurfsharkServerPool(t *testing.T) {
	servers := []surfshark.Server{
		{Country: "US", CountryCode: "US", Location: "New York", ConnectionName: "us-nyc.prod.surfshark.com", PubKey: "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="},
		{Country: "DE", CountryCode: "DE", Location: "Berlin", ConnectionName: "de-ber.prod.surfshark.com", PubKey: "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="},
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(servers) }))
	defer fake.Close()
	st := newStack(t, fake.URL)
	st.srv.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "us-nyc.prod.surfshark.com" {
			return []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("198.51.100.1")}, nil
	}
	st.call(st.client, "POST", "/api/setup", map[string]string{"email": "o@example.com", "password": "a-long-password"}, nil)
	for i := 0; i < 5; i++ {
		k, _ := wgtest.KeyPair()
		st.call(st.client, "POST", "/api/providers/surfshark/keys", map[string]string{"privateKey": k}, nil)
	}
	if code := st.call(st.client, "PUT", "/api/providers/surfshark/selection",
		map[string]any{"lanes": 2, "allServers": true, "includeVirtual": true}, nil); code != 200 {
		t.Fatalf("selection: %d", code)
	}
	var lanes []Lane
	st.call(st.client, "GET", "/api/lanes", nil, &lanes)
	// Every server is a lane, interleaved across locations; none is reported yet: standby.
	if ids := laneIDs(lanes); ids != "surfshark:de-ber@198.51.100.1,surfshark:us-nyc@192.0.2.1,surfshark:us-nyc@192.0.2.2" {
		t.Fatalf("pool lanes: %s", ids)
	}
	if lanes[0].Status != protocol.LaneStandby {
		t.Fatalf("status %q, want standby", lanes[0].Status)
	}
	cfg, err := st.srv.buildEngineConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.TargetUp != 2 || len(cfg.Lanes) != 3 || cfg.Lanes[1].Endpoint != "192.0.2.1:51820" || len(cfg.Lanes[0].Keys) != maxKeysPerLane {
		t.Fatalf("engine config: target %d, %+v", cfg.Settings.TargetUp, cfg.Lanes)
	}
	// Removing one server switches it off; its location stays in the pool.
	if code := st.call(st.client, "DELETE", "/api/lanes/"+url.PathEscape("surfshark:us-nyc@192.0.2.2"), nil, nil); code != 204 {
		t.Fatalf("remove server: %d", code)
	}
	st.call(st.client, "GET", "/api/lanes", nil, &lanes)
	if len(lanes) != 3 || lanes[2].Enabled {
		t.Fatalf("after remove: %+v", lanes)
	}
}
