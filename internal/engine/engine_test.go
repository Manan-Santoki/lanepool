package engine

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/auth"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/wg/wgtest"
)

// fakeControl serves one config and collects reports. While down is set it
// rejects reports, like a control plane that is being redeployed.
type fakeControl struct {
	cfg     protocol.EngineConfig
	down    atomic.Bool
	mu      sync.Mutex
	reports []protocol.Report
}

func (f *fakeControl) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer secret" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/internal/engine/config":
		if r.Header.Get("If-None-Match") == f.cfg.Version {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		json.NewEncoder(w).Encode(f.cfg)
	case "/internal/engine/report":
		if f.down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var rep protocol.Report
		json.NewDecoder(r.Body).Decode(&rep)
		f.mu.Lock()
		f.reports = append(f.reports, rep)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}
}

func (f *fakeControl) all() []protocol.Report {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Report(nil), f.reports...)
}

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func TestEngineEndToEnd(t *testing.T) {
	priv, pub := wgtest.KeyPair()
	prov := wgtest.Start(t, "203.0.113.50", pub)
	hash, _ := auth.HashPassword("password123")
	s := protocol.DefaultSettings()
	s.LaneStartDelay, s.ConnectTimeout = 0, 5

	fc := &fakeControl{cfg: protocol.EngineConfig{
		Version:  "v1",
		Settings: s,
		Lanes: []protocol.LaneSpec{{
			ID: "test:a", Name: "a", CountryCode: "US", Enabled: true, Endpoint: prov.Endpoint, PeerKey: prov.PublicKey,
			Addresses: []string{wgtest.ClientAddr.String()}, DNS: []string{wgtest.DNSAddr.String()},
			Keys: []protocol.LaneKey{{ID: 7, PrivateKey: priv}},
		}},
		Users: []protocol.UserSpec{{ID: 3, Username: "erin", PasswordHash: hash, Enabled: true, LogDestinations: true}},
	}}
	ctl := httptest.NewServer(fc)
	defer ctl.Close()

	proxyAddr, apiAddr := freePort(t), freePort(t)
	e := New(Config{
		ControlURL: ctl.URL, Token: "secret", ProxyAddr: proxyAddr, APIAddr: apiAddr,
		PollInterval: 200 * time.Millisecond, ReportInterval: 100 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	waitFor(t, "lane up in a report", func() bool {
		r := fc.all()
		return len(r) > 0 && len(r[len(r)-1].Lanes) == 1 && r[len(r)-1].Lanes[0].Status == protocol.LaneUp
	})

	// Control goes away; traffic keeps flowing and nothing is lost.
	fc.down.Store(true)
	pu := &url.URL{Scheme: "http", Host: proxyAddr, User: url.UserPassword("erin", "password123")}
	hc := http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true}}
	resp, err := hc.Get("http://api.ipify.org/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(body)) != "203.0.113.50" {
		t.Fatalf("exit IP %q", body)
	}
	time.Sleep(300 * time.Millisecond)
	fc.down.Store(false)

	var rec protocol.ConnRecord
	waitFor(t, "connection record and usage", func() bool {
		var usage int64
		for _, r := range fc.all() {
			for _, c := range r.Connections {
				rec = c
			}
			for _, u := range r.Usage {
				usage += u.BytesDown
			}
		}
		return rec.ID != "" && usage > 0
	})
	if rec.Username != "erin" || rec.UserID != 3 || rec.LaneID != "test:a" || rec.ExitIP != "203.0.113.50" || rec.Result != protocol.ResultOK {
		t.Fatalf("record %+v", rec)
	}
	n := 0
	for _, r := range fc.all() {
		n += len(r.Connections)
	}
	if n != 1 {
		t.Fatalf("connection reported %d times", n)
	}

	// Engine API needs the token.
	get := func(path, token string) int {
		req, _ := http.NewRequest("GET", "http://"+apiAddr+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if get("/healthz", "") != 200 || get("/v1/state", "") != 401 || get("/v1/state", "secret") != 200 || get("/metrics", "secret") != 200 {
		t.Fatal("engine API auth is wrong")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
