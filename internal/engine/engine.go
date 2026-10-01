// Package engine runs the lanes and the gateway and talks to control.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Manan-Santoki/lanepool/internal/engine/gateway"
	"github.com/Manan-Santoki/lanepool/internal/engine/lanes"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

// Config configures an engine.
type Config struct {
	NodeID         string
	ControlURL     string // e.g. http://control:8000
	Token          string // shared secret with control
	ProxyAddr      string // gateway listen address, e.g. :8080
	APIAddr        string // engine API listen address, e.g. :9090
	TrustedProxies []netip.Prefix
	PollInterval   time.Duration // config refresh; default 10s
	ReportInterval time.Duration // default 2s
	Start          lanes.StartFunc
}

// Engine is a running engine.
type Engine struct {
	cfg     Config
	log     *slog.Logger
	lanes   *lanes.Manager
	policy  *gateway.Policy
	gw      *gateway.Server
	started time.Time
	client  *http.Client

	mu      sync.Mutex
	version string
	pending protocol.Report // data not yet accepted by control
	reload  chan struct{}
}

// New creates an engine.
func New(cfg Config, log *slog.Logger) *Engine {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 10 * time.Second
	}
	if cfg.ReportInterval == 0 {
		cfg.ReportInterval = 2 * time.Second
	}
	if cfg.NodeID == "" {
		cfg.NodeID = "local"
	}
	m := lanes.New(protocol.DefaultSettings(), cfg.Start)
	p := gateway.NewPolicy()
	return &Engine{
		cfg: cfg, log: log, lanes: m, policy: p, started: time.Now(),
		gw:     &gateway.Server{Policy: p, Lanes: m, Log: log, TrustedProxies: cfg.TrustedProxies},
		client: &http.Client{Timeout: 15 * time.Second},
		reload: make(chan struct{}, 1),
	}
}

// Run starts everything and blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	if err := e.gw.Listen(e.cfg.ProxyAddr); err != nil {
		return fmt.Errorf("proxy listen: %w", err)
	}
	defer e.gw.Close()
	e.log.Info("proxy listening", "addr", e.cfg.ProxyAddr)

	api := &http.Server{Addr: e.cfg.APIAddr, Handler: e.Router(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := api.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			e.log.Error("engine API", "err", err)
		}
	}()
	defer api.Close()
	e.log.Info("engine API listening", "addr", e.cfg.APIAddr)

	e.pending.Events = append(e.pending.Events, protocol.Event{
		Time: time.Now(), Level: "info", Type: protocol.EventEngineStarted, Message: "engine started"})

	go e.lanes.Run(ctx, time.Second)
	go e.configLoop(ctx)
	e.reportLoop(ctx)
	return nil
}

// --- config -----------------------------------------------------------------

func (e *Engine) configLoop(ctx context.Context) {
	t := time.NewTicker(e.cfg.PollInterval)
	defer t.Stop()
	for {
		if err := e.fetchConfig(ctx); err != nil && ctx.Err() == nil {
			e.log.Warn("config fetch failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.reload:
		}
	}
}

func (e *Engine) fetchConfig(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.cfg.ControlURL+"/internal/engine/config", nil)
	req.Header.Set("Authorization", "Bearer "+e.cfg.Token)
	req.Header.Set("X-Lanepool-Node", e.cfg.NodeID)
	e.mu.Lock()
	if e.version != "" {
		req.Header.Set("If-None-Match", e.version)
	}
	e.mu.Unlock()
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("control returned HTTP %d", resp.StatusCode)
	}
	var cfg protocol.EngineConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return err
	}
	e.Apply(cfg)
	return nil
}

// Apply loads a configuration (exported for tests and single-binary setups).
func (e *Engine) Apply(cfg protocol.EngineConfig) {
	e.lanes.Apply(cfg.Settings, cfg.Lanes)
	e.policy.Apply(cfg.Settings, cfg.Users, cfg.Burned)
	e.gw.SetSettings(cfg.Settings)
	e.mu.Lock()
	changed := e.version != cfg.Version
	e.version = cfg.Version
	e.mu.Unlock()
	if changed {
		e.log.Info("config applied", "version", cfg.Version, "lanes", len(cfg.Lanes), "users", len(cfg.Users))
	}
}

// --- reports ----------------------------------------------------------------

func (e *Engine) reportLoop(ctx context.Context) {
	t := time.NewTicker(e.cfg.ReportInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			e.sendReport(sctx) // flush what's left
			cancel()
			return
		case <-t.C:
			if err := e.sendReport(ctx); err != nil && ctx.Err() == nil {
				e.log.Warn("report failed; will retry", "err", err)
			}
		}
	}
}

// Snapshot collects state plus everything drained since the last report.
func (e *Engine) snapshot() protocol.Report {
	recs, burned := e.gw.DrainRecords()
	e.mu.Lock()
	defer e.mu.Unlock()
	r := &e.pending
	r.Connections = append(r.Connections, recs...)
	r.Usage = mergeUsage(r.Usage, e.policy.DrainUsage())
	r.Events = append(r.Events, e.lanes.DrainEvents()...)
	r.AutoBurned = append(r.AutoBurned, burned...)
	// Bound memory while control is unreachable: keep the newest records.
	if n := len(r.Connections); n > 100000 {
		r.Connections = r.Connections[n-100000:]
	}
	if n := len(r.Events); n > 5000 {
		r.Events = r.Events[n-5000:]
	}
	out := *r
	// Copies: pending keeps changing (and is trimmed) after this report is sent.
	out.Connections = append([]protocol.ConnRecord(nil), r.Connections...)
	out.Usage = append([]protocol.UsageDelta(nil), r.Usage...)
	out.Events = append([]protocol.Event(nil), r.Events...)
	out.AutoBurned = append([]protocol.BurnedIP(nil), r.AutoBurned...)
	out.NodeID = e.cfg.NodeID
	out.StartedAt = e.started
	out.Lanes = e.lanes.States()
	out.Gateway = e.gatewayState()
	return out
}

func (e *Engine) gatewayState() protocol.GatewayState {
	st := protocol.GatewayState{Listening: e.gw.Listening(), ActiveConnections: len(e.gw.Live())}
	if p := e.lanes.PausedUntil(); !p.IsZero() {
		st.PausedUntil = &p
	}
	return st
}

func mergeUsage(a, b []protocol.UsageDelta) []protocol.UsageDelta {
	type k struct {
		u int64
		l string
	}
	idx := map[k]int{}
	for i, d := range a {
		idx[k{d.UserID, d.LaneID}] = i
	}
	for _, d := range b {
		if i, ok := idx[k{d.UserID, d.LaneID}]; ok {
			a[i].BytesUp += d.BytesUp
			a[i].BytesDown += d.BytesDown
			a[i].Connections += d.Connections
			a[i].Failures += d.Failures
			continue
		}
		idx[k{d.UserID, d.LaneID}] = len(a)
		a = append(a, d)
	}
	return a
}

func (e *Engine) sendReport(ctx context.Context) error {
	r := e.snapshot()
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.ControlURL+"/internal/engine/report", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("control returned HTTP %d", resp.StatusCode)
	}
	// Accepted: forget what was sent. Anything drained meanwhile stays pending.
	e.mu.Lock()
	p := &e.pending
	p.Connections = p.Connections[min(len(r.Connections), len(p.Connections)):]
	p.Events = p.Events[min(len(r.Events), len(p.Events)):]
	p.AutoBurned = p.AutoBurned[min(len(r.AutoBurned), len(p.AutoBurned)):]
	p.Usage = subtractUsage(p.Usage, r.Usage)
	e.mu.Unlock()
	if resp.Header.Get("X-Lanepool-Config-Changed") == "1" {
		e.Reload()
	}
	return nil
}

func subtractUsage(cur, sent []protocol.UsageDelta) []protocol.UsageDelta {
	out := cur[:0]
	for _, c := range cur {
		for _, s := range sent {
			if s.UserID == c.UserID && s.LaneID == c.LaneID {
				c.BytesUp -= s.BytesUp
				c.BytesDown -= s.BytesDown
				c.Connections -= s.Connections
				c.Failures -= s.Failures
			}
		}
		if c.BytesUp != 0 || c.BytesDown != 0 || c.Connections != 0 || c.Failures != 0 {
			out = append(out, c)
		}
	}
	return out
}

// Reload asks the config loop to fetch the configuration now.
func (e *Engine) Reload() {
	select {
	case e.reload <- struct{}{}:
	default:
	}
}

// --- API --------------------------------------------------------------------

// Router is the engine's API, used by control.
func (e *Engine) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Liveness only: lanes connect in the background and may take long.
		writeJSON(w, http.StatusOK, map[string]any{"alive": true})
	})
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		ready := len(e.lanes.Usable()) > 0 && e.gw.Listening()
		code := http.StatusOK
		if !ready {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, map[string]any{"ready": ready})
	})
	r.Group(func(r chi.Router) {
		r.Use(e.requireToken)
		r.Handle("/metrics", promhttp.HandlerFor(e.metrics(), promhttp.HandlerOpts{}))
		r.Get("/v1/state", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"lanes": e.lanes.States(), "gateway": e.gatewayState()})
		})
		r.Get("/v1/connections", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, e.gw.Live())
		})
		r.Post("/v1/kick", func(w http.ResponseWriter, req *http.Request) {
			var k protocol.KickRequest
			if err := json.NewDecoder(req.Body).Decode(&k); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]int{"kicked": e.gw.Kick(k)})
		})
		r.Post("/v1/lanes/restart-all", func(w http.ResponseWriter, _ *http.Request) {
			e.lanes.RestartAll()
			writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
		})
		r.Post("/v1/lanes/{id}/restart", func(w http.ResponseWriter, req *http.Request) {
			if err := e.lanes.Restart(chi.URLParam(req, "id")); err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
		})
		r.Post("/v1/gateway/restart", func(w http.ResponseWriter, _ *http.Request) {
			if err := e.gw.Restart(); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			e.mu.Lock()
			e.pending.Events = append(e.pending.Events, protocol.Event{Time: time.Now(), Level: "info",
				Type: protocol.EventGatewayRestart, Message: "proxy server restarted; open connections were closed"})
			e.mu.Unlock()
			writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
		})
		r.Post("/v1/sticky/forget", func(w http.ResponseWriter, req *http.Request) {
			var in struct{ Username, Session string }
			if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
				return
			}
			e.policy.ForgetSticky(in.Username, in.Session)
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		})
		r.Post("/v1/config/reload", func(w http.ResponseWriter, _ *http.Request) {
			e.Reload()
			writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
		})
	})
	return r
}

func (e *Engine) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e.cfg.Token == "" || r.Header.Get("Authorization") != "Bearer "+e.cfg.Token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (e *Engine) metrics() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	laneStatus := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "lanepool_lanes", Help: "Lanes by status."}, []string{"status"})
	active := prometheus.NewGauge(prometheus.GaugeOpts{Name: "lanepool_active_connections", Help: "Open proxy connections."})
	paused := prometheus.NewGauge(prometheus.GaugeOpts{Name: "lanepool_new_lanes_paused", Help: "1 while the circuit breaker blocks new lane connections."})
	reg.MustRegister(laneStatus, active, paused)
	counts := map[string]float64{}
	for _, s := range e.lanes.States() {
		counts[s.Status]++
	}
	for _, st := range []string{protocol.LaneQueued, protocol.LaneConnecting, protocol.LaneUp, protocol.LaneDown, protocol.LaneBackoff, protocol.LaneDisabled} {
		laneStatus.WithLabelValues(st).Set(counts[st])
	}
	active.Set(float64(len(e.gw.Live())))
	if !e.lanes.PausedUntil().IsZero() {
		paused.Set(1)
	}
	return reg
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// ParseCIDRs parses a comma separated list of prefixes or addresses.
func ParseCIDRs(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p)
			continue
		}
		a, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q", part)
		}
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}
