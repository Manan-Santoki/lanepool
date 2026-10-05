// Package lanesvc runs the lanes (WireGuard tunnels) in their own process.
//
// The engine's gateway, policy and control sync change often; the tunnels
// rarely do. With the lanes in a separate process ("lanepool lanes"), an engine
// redeploy only drops open proxy connections for a few seconds: the tunnels and
// their Surfshark sessions stay up, and no burst of new handshakes is sent.
//
// The lanes process serves the lane manager over HTTP: GET /v1/state, POST
// /v1/apply, POST /v1/restart and /v1/restart-all, and CONNECT host:port with an
// X-Lane header to dial through a lane. Every request carries the shared token.
// The engine uses Client, which implements the same methods as lanes.Manager.
package lanesvc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/engine/lanes"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

// State is the lanes process's snapshot, polled by the engine.
type State struct {
	Started     time.Time            `json:"started"` // changes when the lanes process restarts
	Usable      []lanes.Info         `json:"usable"`
	States      []protocol.LaneState `json:"states"`
	PausedUntil time.Time            `json:"pausedUntil"`
	Events      []protocol.Event     `json:"events,omitempty"` // drained: each event is sent once
}

type applyRequest struct {
	Settings protocol.EngineSettings `json:"settings"`
	Lanes    []protocol.LaneSpec     `json:"lanes"`
}

// --- server -----------------------------------------------------------------

// Server serves a lane manager to an engine.
type Server struct {
	m       *lanes.Manager
	token   string
	log     *slog.Logger
	started time.Time
}

// NewServer wraps m. token must be non-empty.
func NewServer(m *lanes.Manager, token string, log *slog.Logger) *Server {
	return &Server{m: m, token: token, log: log, started: time.Now().UTC()}
}

func (s *Server) authorized(r *http.Request) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return s.token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodConnect:
		s.connect(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/state":
		st := State{Started: s.started, Usable: s.m.Usable(), States: s.m.States(), PausedUntil: s.m.PausedUntil(), Events: s.m.DrainEvents()}
		writeJSON(w, st)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/apply":
		var req applyRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<20)).Decode(&req); err != nil {
			http.Error(w, "bad config: "+err.Error(), http.StatusBadRequest)
			return
		}
		s.m.Apply(req.Settings, req.Lanes)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/restart":
		if err := s.m.Restart(r.URL.Query().Get("id")); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/restart-all":
		s.m.RestartAll()
		w.WriteHeader(http.StatusAccepted)
	default:
		http.NotFound(w, r)
	}
}

// connect dials r.Host through the lane named in X-Lane and splices the
// hijacked client connection to it.
func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	upstream, release, err := s.m.Dial(ctx, r.Header.Get("X-Lane"), r.Host)
	cancel()
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, lanes.ErrNoLane) {
			code = http.StatusServiceUnavailable
		}
		http.Error(w, err.Error(), code)
		return
	}
	defer release()
	defer upstream.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	if _, err := io.WriteString(client, "HTTP/1.1 200 OK\r\n\r\n"); err != nil {
		return
	}
	if n := buf.Reader.Buffered(); n > 0 { // bytes the client sent right after the request
		b, _ := buf.Reader.Peek(n)
		if _, err := upstream.Write(b); err != nil {
			return
		}
	}
	splice(client, upstream)
}

// splice copies both ways until either side is done, then closes both.
func splice(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	a.Close()
	b.Close()
	<-done
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// --- client -----------------------------------------------------------------

// Client is the engine's view of a lanes process. Usable, States and the rest
// answer from a snapshot polled by Run, so the gateway never waits on the
// network to pick a lane.
type Client struct {
	base  string // http://host:port
	host  string // host:port, for CONNECT
	token string
	http  *http.Client
	log   *slog.Logger

	mu       sync.Mutex
	state    State
	events   []protocol.Event
	lastOK   time.Time
	cfg      *applyRequest // last config from control; resent until accepted
	applied  bool
	sentToAt time.Time // the lanes process start time the config was sent to
}

// NewClient returns a client for the lanes process at rawURL (http://host:port).
func NewClient(rawURL, token string, log *slog.Logger) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "") {
		return nil, fmt.Errorf("lanes URL %q: want http://host:port", rawURL)
	}
	if token == "" {
		return nil, errors.New("lanes token is empty")
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "9191")
	}
	return &Client{base: "http://" + host, host: host, token: token, log: log,
		http: &http.Client{Timeout: 10 * time.Second}}, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("lanes %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Apply hands control's lane configuration to the lanes process. It is kept and
// resent if the send fails or the lanes process restarts.
func (c *Client) Apply(settings protocol.EngineSettings, specs []protocol.LaneSpec) {
	c.mu.Lock()
	c.cfg = &applyRequest{Settings: settings, Lanes: specs}
	c.applied = false
	c.mu.Unlock()
	c.sendConfig(context.Background())
}

func (c *Client) sendConfig(ctx context.Context) {
	c.mu.Lock()
	cfg, started := c.cfg, c.state.Started
	c.mu.Unlock()
	if cfg == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.do(ctx, http.MethodPost, "/v1/apply", cfg, nil); err != nil {
		c.log.Warn("lanes: config not delivered; will retry", "err", err)
		return
	}
	c.mu.Lock()
	if c.cfg == cfg {
		c.applied, c.sentToAt = true, started
	}
	c.mu.Unlock()
}

// Run polls the lanes process every tick and redelivers the configuration
// when needed.
func (c *Client) Run(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		c.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// staleAfter is how long the last snapshot is trusted while polls fail.
const staleAfter = 5 * time.Second

func (c *Client) poll(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	var st State
	err := c.do(pctx, http.MethodGet, "/v1/state", nil, &st)
	cancel()
	c.mu.Lock()
	if err != nil {
		if time.Since(c.lastOK) > staleAfter && len(c.state.Usable) > 0 {
			c.log.Warn("lanes process unreachable; no usable lanes until it answers", "err", err)
			c.state.Usable = nil
		}
		c.mu.Unlock()
		return
	}
	restarted := c.applied && !c.sentToAt.IsZero() && !st.Started.Equal(c.sentToAt)
	if restarted || (c.applied && c.sentToAt.IsZero()) {
		c.applied = false // the lanes process restarted (or the first send predates any poll): resend
	}
	c.events = append(c.events, st.Events...)
	if n := len(c.events); n > 5000 {
		c.events = c.events[n-5000:]
	}
	st.Events = nil
	c.state, c.lastOK = st, time.Now()
	needSend := !c.applied && c.cfg != nil
	c.mu.Unlock()
	if needSend {
		c.sendConfig(ctx)
	}
}

// Usable lists lanes that can carry connections (from the last poll).
func (c *Client) Usable() []lanes.Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]lanes.Info(nil), c.state.Usable...)
}

// States reports every lane (from the last poll).
func (c *Client) States() []protocol.LaneState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]protocol.LaneState(nil), c.state.States...)
}

// PausedUntil reports the lane breaker (from the last poll).
func (c *Client) PausedUntil() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.PausedUntil
}

// DrainEvents returns lane events received since the last call.
func (c *Client) DrainEvents() []protocol.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.events
	c.events = nil
	return out
}

// Restart reconnects one lane.
func (c *Client) Restart(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.do(ctx, http.MethodPost, "/v1/restart?id="+url.QueryEscape(id), nil, nil)
}

// RestartAll reconnects every lane (paced by the lanes process).
func (c *Client) RestartAll() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.do(ctx, http.MethodPost, "/v1/restart-all", nil, nil); err != nil {
		c.log.Warn("lanes: restart-all failed", "err", err)
	}
}

// Dial connects to address through lane id, via the lanes process.
func (c *Client) Dial(ctx context.Context, id, address string) (net.Conn, func(), error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.host)
	if err != nil {
		return nil, nil, fmt.Errorf("lanes process: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nX-Lane: %s\r\nAuthorization: Bearer %s\r\n\r\n", address, address, id, c.token)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("lanes process: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		conn.Close()
		if resp.StatusCode == http.StatusServiceUnavailable {
			return nil, nil, fmt.Errorf("%w: %s", lanes.ErrNoLane, strings.TrimSpace(string(msg)))
		}
		return nil, nil, fmt.Errorf("dial via lane %s: %s", id, strings.TrimSpace(string(msg)))
	}
	conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufConn{Conn: conn, r: br}, func() {}, nil
	}
	return conn, func() {}, nil
}

// bufConn reads bytes already buffered after the CONNECT response first.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }
