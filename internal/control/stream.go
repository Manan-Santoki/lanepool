package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

// hub fans out live updates to dashboard SSE streams.
type hub struct {
	mu   sync.Mutex
	subs map[chan sseMsg]struct{}
}

type sseMsg struct {
	event string
	data  []byte
}

func newHub() *hub { return &hub{subs: map[chan sseMsg]struct{}{}} }

func (h *hub) subscribe() chan sseMsg {
	ch := make(chan sseMsg, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan sseMsg) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *hub) publish(event string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- sseMsg{event, data}:
		default: // slow client: drop rather than block ingestion
		}
	}
}

func (h *hub) publishEvent(e Event) { h.publish("event", e) }

// streamStatePayload is sent as the "state" SSE event.
type streamStatePayload struct {
	Lanes             []Lane                `json:"lanes"`
	Gateway           protocol.GatewayState `json:"gateway"`
	Engine            EngineStatus          `json:"engine"`
	ActiveConnections int                   `json:"activeConnections"`
	// Partial is set when standby lanes are left out (server pools are large);
	// lanes missing from Lanes are on standby.
	Partial bool `json:"partial,omitempty"`
}

func (h *hub) publishState(p *streamStatePayload) {
	if p != nil {
		h.publish("state", p)
	}
}

func (s *Server) streamState() *streamStatePayload {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	lanes, err := s.listLanesData(ctx)
	if err != nil {
		return nil
	}
	_, gw := s.latestLaneStates()
	p := &streamStatePayload{Lanes: lanes, Gateway: gw, Engine: s.engineStatus(), ActiveConnections: gw.ActiveConnections}
	if sel, err := s.selection(ctx); err == nil && sel.AllServers {
		p.Partial, p.Lanes = true, []Lane{}
		for _, l := range lanes {
			if l.Status != protocol.LaneStandby {
				p.Lanes = append(p.Lanes, l)
			}
		}
	}
	return p
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)

	send := func(event string, data []byte) bool {
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if st := s.streamState(); st != nil {
		data, _ := json.Marshal(st)
		send("state", data)
	}
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-ch:
			if !send(m.event, m.data) {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// --- overview -------------------------------------------------------------------

func (s *Server) overview(_ http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	lanes, err := s.listLanesData(ctx)
	if err != nil {
		return nil, err
	}
	byStatus := map[string]int{}
	exit := map[string]bool{}
	for _, l := range lanes {
		byStatus[l.Status]++
		if l.ExitIP != "" && l.Status == protocol.LaneUp {
			exit[l.ExitIP] = true
		}
	}
	_, gw := s.latestLaneStates()
	settings, err := s.engineSettings(ctx)
	if err != nil {
		return nil, err
	}
	var traffic trafficPoint
	if err := s.db.QueryRow(ctx, `SELECT COALESCE(sum(bytes_up),0)::bigint, COALESCE(sum(bytes_down),0)::bigint,
		COALESCE(sum(connections),0)::bigint, COALESCE(sum(failures),0)::bigint
		FROM usage_hourly WHERE hour >= now() - interval '24 hours'`).
		Scan(&traffic.BytesUp, &traffic.BytesDown, &traffic.Connections, &traffic.Failures); err != nil {
		return nil, err
	}
	var total, enabled int
	if err := s.db.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE enabled) FROM proxy_users`).Scan(&total, &enabled); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT id, time, level, type, lane_id, actor, message FROM events ORDER BY id DESC LIMIT 10`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recent := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Time, &e.Level, &e.Type, &e.LaneID, &e.Actor, &e.Message); err != nil {
			return nil, err
		}
		recent = append(recent, e)
	}
	return map[string]any{
		"lanes":             map[string]any{"total": len(lanes), "byStatus": byStatus},
		"uniqueExitIps":     len(exit),
		"activeConnections": gw.ActiveConnections,
		"gateway":           gw,
		"maintenance":       settings.Paused,
		"engine":            s.engineStatus(),
		"traffic24h": map[string]int64{"bytesUp": traffic.BytesUp, "bytesDown": traffic.BytesDown,
			"connections": traffic.Connections, "failures": traffic.Failures},
		"users":        map[string]int{"total": total, "enabled": enabled},
		"recentEvents": recent,
	}, nil
}
