package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

// engineClient calls the engine's API.
type engineClient struct {
	url   string
	token string
	http  *http.Client
}

func (c *engineClient) do(ctx context.Context, method, path string, body, out any) error {
	if c.url == "" {
		return fmt.Errorf("no engine configured")
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("engine unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct{ Error string }
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("engine: %s (HTTP %d)", e.Error, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *engineClient) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *engineClient) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

// engineErr turns an engine failure into a 502 for the dashboard.
func engineErr(err error) error {
	if err == nil {
		return nil
	}
	return errStatus(http.StatusFailedDependency, err.Error())
}

// --- configuration for the engine -------------------------------------------

// buildEngineConfig assembles the engine's configuration from the database.
func (s *Server) buildEngineConfig(ctx context.Context) (protocol.EngineConfig, error) {
	var cfg protocol.EngineConfig
	settings, err := s.engineSettings(ctx)
	if err != nil {
		return cfg, err
	}
	cfg.Settings = settings
	sel, err := s.selection(ctx)
	if err != nil {
		return cfg, err
	}
	if sel.AllServers {
		cfg.Settings.TargetUp = max(sel.Lanes, 1)
	}
	if cfg.Lanes, err = s.laneSpecs(ctx); err != nil {
		return cfg, err
	}
	users, err := s.loadUsers(ctx)
	if err != nil {
		return cfg, err
	}
	for _, u := range users {
		cfg.Users = append(cfg.Users, u.spec())
	}
	rows, err := s.db.Query(ctx, `SELECT domain, lane_id, expires_at FROM burned_ips WHERE expires_at > now()`)
	if err != nil {
		return cfg, err
	}
	cfg.Burned, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (protocol.BurnedIP, error) {
		var b protocol.BurnedIP
		err := row.Scan(&b.Domain, &b.LaneID, &b.ExpiresAt)
		return b, err
	})
	if err != nil {
		return cfg, err
	}
	// The version is a hash of the content, so engines only reload on real changes.
	raw, _ := json.Marshal(cfg)
	sum := sha256.Sum256(raw)
	cfg.Version = hex.EncodeToString(sum[:8])
	return cfg, nil
}

func (s *Server) handleEngineConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.buildEngineConfig(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if r.Header.Get("If-None-Match") == cfg.Version {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", cfg.Version)
	writeJSON(w, http.StatusOK, cfg)
}

// --- reports from the engine --------------------------------------------------

func (s *Server) handleEngineReport(w http.ResponseWriter, r *http.Request) {
	var rep protocol.Report
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&rep); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid report"})
		return
	}
	if err := s.ingest(r.Context(), &rep); err != nil {
		s.log.Error("ingest engine report", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ingest failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ingest(ctx context.Context, rep *protocol.Report) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if len(rep.Connections) > 0 {
		rows := make([][]any, 0, len(rep.Connections))
		for _, c := range rep.Connections {
			var uid *int64
			if c.UserID != 0 {
				id := c.UserID
				uid = &id
			}
			rows = append(rows, []any{c.ID, c.StartedAt, c.EndedAt, uid, c.Username, c.ClientIP, c.Target,
				c.LaneID, c.ExitIP, c.Protocol, c.BytesUp, c.BytesDown, c.Result, truncate(c.Error, 500)})
		}
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"connection_logs"},
			[]string{"id", "started_at", "ended_at", "user_id", "username", "client_ip", "target", "lane_id",
				"exit_ip", "protocol", "bytes_up", "bytes_down", "result", "error"},
			pgx.CopyFromRows(rows))
		if err != nil {
			return fmt.Errorf("insert connection logs: %w", err)
		}
	}

	hour := time.Now().UTC().Truncate(time.Hour)
	for _, u := range rep.Usage {
		if u.UserID == 0 {
			continue
		}
		_, err := tx.Exec(ctx, `INSERT INTO usage_hourly (hour, user_id, lane_id, bytes_up, bytes_down, connections, failures)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (hour, user_id, lane_id) DO UPDATE SET
				bytes_up = usage_hourly.bytes_up + EXCLUDED.bytes_up,
				bytes_down = usage_hourly.bytes_down + EXCLUDED.bytes_down,
				connections = usage_hourly.connections + EXCLUDED.connections,
				failures = usage_hourly.failures + EXCLUDED.failures`,
			hour, u.UserID, u.LaneID, u.BytesUp, u.BytesDown, u.Connections, u.Failures)
		if err != nil {
			return fmt.Errorf("usage: %w", err)
		}
		_, err = tx.Exec(ctx, `UPDATE proxy_users SET used_bytes = used_bytes + $2,
			last_seen_at = CASE WHEN $3 > 0 THEN now() ELSE last_seen_at END WHERE id = $1`,
			u.UserID, u.BytesUp+u.BytesDown, u.Connections+u.BytesUp+u.BytesDown)
		if err != nil {
			return fmt.Errorf("user usage: %w", err)
		}
	}

	for _, e := range rep.Events {
		if _, err := tx.Exec(ctx, `INSERT INTO events (time, level, type, lane_id, actor, message) VALUES ($1, $2, $3, $4, 'engine', $5)`,
			e.Time, e.Level, e.Type, e.LaneID, e.Message); err != nil {
			return fmt.Errorf("events: %w", err)
		}
	}

	exitIPs := map[string]string{}
	for _, l := range rep.Lanes {
		exitIPs[l.ID] = l.ExitIP
	}
	for _, b := range rep.AutoBurned {
		if _, err := tx.Exec(ctx, `INSERT INTO burned_ips (domain, lane_id, exit_ip, source, note, expires_at)
			VALUES ($1, $2, $3, 'auto', 'repeated connection failures', $4)`, b.Domain, b.LaneID, exitIPs[b.LaneID], b.ExpiresAt); err != nil {
			return fmt.Errorf("burned: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO events (level, type, lane_id, actor, message) VALUES ('warn', $1, $2, 'engine', $3)`,
			protocol.EventAutoBurned, b.LaneID, fmt.Sprintf("lane avoided for %s after repeated connection failures", b.Domain)); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	s.report = rep
	s.reportedAt = time.Now()
	s.mu.Unlock()

	s.hub.publishState(s.streamState())
	for _, e := range rep.Events {
		s.hub.publishEvent(Event{Time: e.Time, Level: e.Level, Type: e.Type, LaneID: e.LaneID, Actor: "engine", Message: e.Message})
	}
	s.alerting.onReport(ctx, rep)
	return nil
}

// EngineStatus summarises the connection to the engine.
type EngineStatus struct {
	Connected    bool       `json:"connected"`
	NodeID       string     `json:"nodeId,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	LastReportAt *time.Time `json:"lastReportAt,omitempty"`
}

func (s *Server) engineStatus() EngineStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.report == nil {
		return EngineStatus{}
	}
	at, started := s.reportedAt, s.report.StartedAt
	return EngineStatus{Connected: time.Since(at) < 30*time.Second, NodeID: s.report.NodeID, StartedAt: &started, LastReportAt: &at}
}

// latestLaneStates returns lane runtime state by lane ID from the last report.
func (s *Server) latestLaneStates() (map[string]protocol.LaneState, protocol.GatewayState) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]protocol.LaneState{}
	if s.report == nil {
		return out, protocol.GatewayState{}
	}
	for _, l := range s.report.Lanes {
		out[l.ID] = l
	}
	return out, s.report.Gateway
}

// --- live connections and controls ---------------------------------------------

func (s *Server) liveConnections(_ http.ResponseWriter, r *http.Request) (any, error) {
	var live []protocol.LiveConn
	if err := s.engine.get(r.Context(), "/v1/connections", &live); err != nil {
		return nil, engineErr(err)
	}
	if live == nil {
		live = []protocol.LiveConn{}
	}
	return live, nil
}

func (s *Server) kick(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in protocol.KickRequest
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	var out struct {
		Kicked int `json:"kicked"`
	}
	if err := s.engine.post(r.Context(), "/v1/kick", in, &out); err != nil {
		return nil, engineErr(err)
	}
	s.audit(r.Context(), who(r), "admin.kick", fmt.Sprintf("closed %d proxy connection(s)", out.Kicked))
	return out, nil
}

func (s *Server) restartGateway(_ http.ResponseWriter, r *http.Request) (any, error) {
	if err := s.engine.post(r.Context(), "/v1/gateway/restart", nil, nil); err != nil {
		return nil, engineErr(err)
	}
	s.audit(r.Context(), who(r), "admin.gateway_restart", "restarted the proxy server")
	return status(http.StatusAccepted), nil
}

func (s *Server) rotateSession(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in struct{ Username, Session string }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.Username == "" || in.Session == "" {
		return nil, errFields(map[string]string{"session": "username and session are required"})
	}
	if err := s.engine.post(r.Context(), "/v1/sticky/forget", in, nil); err != nil {
		return nil, engineErr(err)
	}
	return nil, nil
}
