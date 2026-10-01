package control

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// BurnedIP marks a lane as blocked by a domain.
type BurnedIP struct {
	ID        int64     `json:"id"`
	Domain    string    `json:"domain"`
	LaneID    string    `json:"laneId"`
	LaneName  string    `json:"laneName,omitempty"`
	ExitIP    string    `json:"exitIp,omitempty"`
	Source    string    `json:"source"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

const burnedCols = `b.id, b.domain, b.lane_id, COALESCE(l.name, ''), b.exit_ip, b.source, b.note, b.created_at, b.expires_at`

func scanBurned(row pgx.Row) (BurnedIP, error) {
	var b BurnedIP
	err := row.Scan(&b.ID, &b.Domain, &b.LaneID, &b.LaneName, &b.ExitIP, &b.Source, &b.Note, &b.CreatedAt, &b.ExpiresAt)
	return b, err
}

func (s *Server) listBurned(_ http.ResponseWriter, r *http.Request) (any, error) {
	rows, err := s.db.Query(r.Context(), `SELECT `+burnedCols+` FROM burned_ips b LEFT JOIN lanes l ON l.id = b.lane_id
		WHERE b.expires_at > now() - interval '1 day' ORDER BY b.created_at DESC LIMIT 2000`)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (BurnedIP, error) { return scanBurned(row) })
	if items == nil {
		items = []BurnedIP{}
	}
	return items, err
}

type burnInput struct {
	Domain     string `json:"domain"`
	LaneID     string `json:"laneId"`
	ExitIP     string `json:"exitIp"`
	TTLMinutes int    `json:"ttlMinutes"`
	Note       string `json:"note"`
}

var burnDomainRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

func (s *Server) burn(ctx context.Context, in burnInput, source string) (BurnedIP, error) {
	in.Domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(in.Domain)), "*.")
	f := map[string]string{}
	if !burnDomainRe.MatchString(in.Domain) {
		f["domain"] = "enter a domain like example.com"
	}
	if in.TTLMinutes == 0 {
		in.TTLMinutes = 60
	}
	if in.TTLMinutes < 1 || in.TTLMinutes > 60*24*90 {
		f["ttlMinutes"] = "between 1 minute and 90 days"
	}
	// Resolve the lane from its exit IP if needed.
	if in.LaneID == "" && in.ExitIP != "" {
		states, _ := s.latestLaneStates()
		for id, st := range states {
			if st.ExitIP == in.ExitIP {
				in.LaneID = id
			}
		}
		if in.LaneID == "" {
			f["exitIp"] = "no lane currently has this exit IP"
		}
	}
	if in.LaneID == "" && in.ExitIP == "" {
		f["laneId"] = "choose a lane or enter an exit IP"
	}
	if len(f) > 0 {
		return BurnedIP{}, errFields(f)
	}
	if in.ExitIP == "" {
		states, _ := s.latestLaneStates()
		in.ExitIP = states[in.LaneID].ExitIP
	}
	b, err := scanBurned(s.db.QueryRow(ctx, `WITH b AS (
			INSERT INTO burned_ips (domain, lane_id, exit_ip, source, note, expires_at)
			VALUES ($1, $2, $3, $4, $5, now() + make_interval(mins => $6)) RETURNING *)
		SELECT `+burnedCols+` FROM b LEFT JOIN lanes l ON l.id = b.lane_id`,
		in.Domain, in.LaneID, in.ExitIP, source, strings.TrimSpace(in.Note), in.TTLMinutes))
	if err != nil {
		return b, err
	}
	s.configChanged()
	return b, nil
}

func (s *Server) createBurned(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in burnInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	b, err := s.burn(r.Context(), in, "manual")
	if err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.burned", "marked lane "+b.LaneID+" as blocked by "+b.Domain)
	return b, nil
}

func (s *Server) apiBurn(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in burnInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	b, err := s.burn(r.Context(), in, "api")
	if err != nil {
		return nil, err
	}
	s.addEvent(r.Context(), "info", "burned_api", b.LaneID, who(r).name(), "lane "+b.LaneID+" reported as blocked by "+b.Domain)
	return b, nil
}

func (s *Server) deleteBurned(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	var domain, lane string
	if err := s.db.QueryRow(r.Context(), `DELETE FROM burned_ips WHERE id = $1 RETURNING domain, lane_id`, id).Scan(&domain, &lane); err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.burned_removed", "lane "+lane+" no longer avoided for "+domain)
	s.configChanged()
	return nil, nil
}
