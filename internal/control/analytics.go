package control

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func rangeParam(r *http.Request) (time.Duration, string, error) {
	switch v := r.URL.Query().Get("range"); v {
	case "", "24h":
		return 24 * time.Hour, "hour", nil
	case "7d":
		return 7 * 24 * time.Hour, "hour", nil
	case "30d":
		return 30 * 24 * time.Hour, "day", nil
	case "90d":
		return 90 * 24 * time.Hour, "day", nil
	default:
		return 0, "", errStatus(http.StatusBadRequest, "range must be 24h, 7d, 30d or 90d")
	}
}

type trafficPoint struct {
	T           time.Time `json:"t"`
	BytesUp     int64     `json:"bytesUp"`
	BytesDown   int64     `json:"bytesDown"`
	Connections int64     `json:"connections"`
	Failures    int64     `json:"failures"`
}

func (s *Server) traffic(_ http.ResponseWriter, r *http.Request) (any, error) {
	span, bucket, err := rangeParam(r)
	if err != nil {
		return nil, err
	}
	step := time.Hour
	if bucket == "day" {
		step = 24 * time.Hour
	}
	end := time.Now().UTC().Truncate(step)
	start := end.Add(-span + step)
	w := &where{}
	w.add("hour >= ?", start)
	if v := r.URL.Query().Get("user"); v != "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		w.add("user_id = ?", id)
	}
	if v := r.URL.Query().Get("lane"); v != "" {
		w.add("lane_id = ?", v)
	}
	rows, err := s.db.Query(r.Context(), fmt.Sprintf(`SELECT date_trunc('%s', hour) AS t, sum(bytes_up)::bigint, sum(bytes_down)::bigint, sum(connections)::bigint, sum(failures)::bigint
		FROM usage_hourly%s GROUP BY 1`, bucket, w.sql()), w.args...)
	if err != nil {
		return nil, err
	}
	got, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (trafficPoint, error) {
		var p trafficPoint
		err := row.Scan(&p.T, &p.BytesUp, &p.BytesDown, &p.Connections, &p.Failures)
		return p, err
	})
	if err != nil {
		return nil, err
	}
	byT := map[int64]trafficPoint{}
	for _, p := range got {
		byT[p.T.UTC().Unix()] = p
	}
	// Fill gaps so charts have a point per bucket.
	var out []trafficPoint
	for t := start; !t.After(end); t = t.Add(step) {
		p, ok := byT[t.Unix()]
		if !ok {
			p = trafficPoint{T: t}
		}
		out = append(out, p)
	}
	return out, nil
}

type topItem struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Bytes       int64  `json:"bytes"`
	Connections int64  `json:"connections"`
	Failures    int64  `json:"failures"`
}

func (s *Server) top(_ http.ResponseWriter, r *http.Request) (any, error) {
	span, _, err := rangeParam(r)
	if err != nil {
		return nil, err
	}
	since := time.Now().Add(-span)
	limit := limitParam(r.URL.Query(), 10, 100)
	var query string
	switch r.URL.Query().Get("by") {
	case "", "user":
		query = `SELECT u.user_id::text, COALESCE(p.username, 'deleted user'), sum(bytes_up + bytes_down)::bigint, sum(connections)::bigint, sum(failures)::bigint
			FROM usage_hourly u LEFT JOIN proxy_users p ON p.id = u.user_id WHERE hour >= $1
			GROUP BY 1, 2 ORDER BY 3 DESC LIMIT $2`
	case "lane":
		query = `SELECT u.lane_id, COALESCE(l.name, u.lane_id), sum(bytes_up + bytes_down)::bigint, sum(connections)::bigint, sum(failures)::bigint
			FROM usage_hourly u LEFT JOIN lanes l ON l.id = u.lane_id WHERE hour >= $1
			GROUP BY 1, 2 ORDER BY 3 DESC LIMIT $2`
	case "country":
		query = `SELECT COALESCE(NULLIF(l.country_code, ''), '??'), COALESCE(NULLIF(max(l.country), ''), 'Unknown'),
			sum(bytes_up + bytes_down)::bigint, sum(connections)::bigint, sum(failures)::bigint
			FROM usage_hourly u LEFT JOIN lanes l ON l.id = u.lane_id WHERE hour >= $1
			GROUP BY 1 ORDER BY 3 DESC LIMIT $2`
	case "domain":
		// Only connections whose destinations were logged, within log retention.
		query = `SELECT host, host, sum(bytes_up + bytes_down)::bigint, count(*), count(*) FILTER (WHERE result <> 'ok')
			FROM (SELECT split_part(target, ':', 1) AS host, bytes_up, bytes_down, result FROM connection_logs
				WHERE started_at >= $1 AND target <> '') t
			GROUP BY 1 ORDER BY 4 DESC LIMIT $2`
	default:
		return nil, errStatus(http.StatusBadRequest, "by must be user, lane, country or domain")
	}
	rows, err := s.db.Query(r.Context(), query, since, limit)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (topItem, error) {
		var t topItem
		err := row.Scan(&t.Key, &t.Label, &t.Bytes, &t.Connections, &t.Failures)
		return t, err
	})
	if err != nil {
		return nil, err
	}
	for i := range items {
		if r.URL.Query().Get("by") == "country" {
			items[i].Key = strings.ToLower(items[i].Key)
		}
	}
	if items == nil {
		items = []topItem{}
	}
	return items, nil
}
