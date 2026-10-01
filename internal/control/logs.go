package control

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Event is a system event or audit entry.
type Event struct {
	ID      int64     `json:"id"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Type    string    `json:"type"`
	LaneID  string    `json:"laneId,omitempty"`
	Actor   string    `json:"actor,omitempty"`
	Message string    `json:"message"`
}

func (s *Server) addEvent(ctx context.Context, level, typ, laneID, actor, msg string) {
	var e Event
	err := s.db.QueryRow(ctx, `INSERT INTO events (level, type, lane_id, actor, message) VALUES ($1, $2, $3, $4, $5)
		RETURNING id, time, level, type, lane_id, actor, message`, level, typ, laneID, actor, msg).
		Scan(&e.ID, &e.Time, &e.Level, &e.Type, &e.LaneID, &e.Actor, &e.Message)
	if err != nil {
		s.log.Error("record event", "type", typ, "err", err)
		return
	}
	s.hub.publishEvent(e)
}

// ConnLog is one row of the connection log.
type ConnLog struct {
	ID         string    `json:"id"`
	StartedAt  time.Time `json:"startedAt"`
	EndedAt    time.Time `json:"endedAt"`
	DurationMs int64     `json:"durationMs"`
	UserID     *int64    `json:"userId,omitempty"`
	Username   string    `json:"username,omitempty"`
	ClientIP   string    `json:"clientIp"`
	Target     string    `json:"target,omitempty"`
	LaneID     string    `json:"laneId,omitempty"`
	ExitIP     string    `json:"exitIp,omitempty"`
	Protocol   string    `json:"protocol"`
	BytesUp    int64     `json:"bytesUp"`
	BytesDown  int64     `json:"bytesDown"`
	Result     string    `json:"result"`
	Error      string    `json:"error,omitempty"`
}

type page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// cursor is (time, id) of the last row, for keyset pagination.
type cursor struct {
	T  time.Time `json:"t"`
	ID string    `json:"i"`
}

func encodeCursor(c cursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (*cursor, error) {
	if s == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, errStatus(http.StatusBadRequest, "invalid cursor")
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, errStatus(http.StatusBadRequest, "invalid cursor")
	}
	return &c, nil
}

// where builds a SQL WHERE clause with numbered arguments.
type where struct {
	parts []string
	args  []any
}

func (w *where) add(cond string, args ...any) {
	for _, a := range args {
		w.args = append(w.args, a)
		cond = strings.Replace(cond, "?", "$"+strconv.Itoa(len(w.args)), 1)
	}
	w.parts = append(w.parts, cond)
}

func (w *where) sql() string {
	if len(w.parts) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.parts, " AND ")
}

func parseTime(q url.Values, key string) (*time.Time, error) {
	v := q.Get(key)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, errStatus(http.StatusBadRequest, key+" must be an RFC 3339 time")
	}
	return &t, nil
}

func limitParam(q url.Values, def, max int) int {
	n, err := strconv.Atoi(q.Get("limit"))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, max)
}

func connLogFilter(q url.Values) (*where, error) {
	w := &where{}
	from, err := parseTime(q, "from")
	if err != nil {
		return nil, err
	}
	to, err := parseTime(q, "to")
	if err != nil {
		return nil, err
	}
	if from != nil {
		w.add("started_at >= ?", *from)
	}
	if to != nil {
		w.add("started_at <= ?", *to)
	}
	if v := q.Get("user"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			w.add("user_id = ?", id)
		} else {
			w.add("lower(username) = lower(?)", v)
		}
	}
	if v := q.Get("lane"); v != "" {
		w.add("lane_id = ?", v)
	}
	if v := q.Get("result"); v != "" {
		w.add("result = ?", v)
	}
	if v := q.Get("client"); v != "" {
		w.add("client_ip = ?", v)
	}
	if v := q.Get("q"); v != "" {
		w.add("target ILIKE ?", "%"+strings.ReplaceAll(v, "%", `\%`)+"%")
	}
	return w, nil
}

const connLogCols = `id, started_at, ended_at, user_id, username, client_ip, target, lane_id, exit_ip, protocol, bytes_up, bytes_down, result, error`

func scanConnLog(row pgx.Row) (ConnLog, error) {
	var c ConnLog
	err := row.Scan(&c.ID, &c.StartedAt, &c.EndedAt, &c.UserID, &c.Username, &c.ClientIP, &c.Target, &c.LaneID,
		&c.ExitIP, &c.Protocol, &c.BytesUp, &c.BytesDown, &c.Result, &c.Error)
	c.DurationMs = c.EndedAt.Sub(c.StartedAt).Milliseconds()
	return c, err
}

func (s *Server) connectionLogs(_ http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	w, err := connLogFilter(q)
	if err != nil {
		return nil, err
	}
	cur, err := decodeCursor(q.Get("cursor"))
	if err != nil {
		return nil, err
	}
	if cur != nil {
		w.add("(started_at, id) < (?, ?)", cur.T, cur.ID)
	}
	limit := limitParam(q, 100, 500)
	rows, err := s.db.Query(r.Context(), `SELECT `+connLogCols+` FROM connection_logs`+w.sql()+
		fmt.Sprintf(` ORDER BY started_at DESC, id DESC LIMIT %d`, limit+1), w.args...)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ConnLog, error) { return scanConnLog(row) })
	if err != nil {
		return nil, err
	}
	out := page[ConnLog]{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		last := out.Items[limit-1]
		out.NextCursor = encodeCursor(cursor{T: last.StartedAt, ID: last.ID})
	}
	if out.Items == nil {
		out.Items = []ConnLog{}
	}
	return out, nil
}

func (s *Server) connectionLogsCSV(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, err := connLogFilter(q)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT `+connLogCols+` FROM connection_logs`+f.sql()+` ORDER BY started_at DESC, id DESC LIMIT 1000000`, f.args...)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="lanepool-connections-%s.csv"`, time.Now().UTC().Format("20060102-150405")))
	cw := csv.NewWriter(w)
	cw.Write([]string{"started_at", "ended_at", "duration_ms", "username", "client_ip", "target", "lane", "exit_ip", "protocol", "bytes_up", "bytes_down", "result", "error"})
	for rows.Next() {
		c, err := scanConnLog(rows)
		if err != nil {
			return
		}
		cw.Write([]string{c.StartedAt.UTC().Format(time.RFC3339Nano), c.EndedAt.UTC().Format(time.RFC3339Nano),
			strconv.FormatInt(c.DurationMs, 10), csvSafe(c.Username), c.ClientIP, csvSafe(c.Target), c.LaneID, c.ExitIP, c.Protocol,
			strconv.FormatInt(c.BytesUp, 10), strconv.FormatInt(c.BytesDown, 10), c.Result, csvSafe(c.Error)})
	}
	cw.Flush()
}

// csvSafe stops spreadsheet formula injection from values clients control.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func (s *Server) listEvents(_ http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	w := &where{}
	from, err := parseTime(q, "from")
	if err != nil {
		return nil, err
	}
	to, err := parseTime(q, "to")
	if err != nil {
		return nil, err
	}
	if from != nil {
		w.add("time >= ?", *from)
	}
	if to != nil {
		w.add("time <= ?", *to)
	}
	if v := q.Get("level"); v != "" {
		w.add("level = ?", v)
	}
	if v := q.Get("type"); v != "" {
		w.add("type LIKE ?", strings.ReplaceAll(v, "%", `\%`)+"%")
	}
	if v := q.Get("lane"); v != "" {
		w.add("lane_id = ?", v)
	}
	if v := q.Get("q"); v != "" {
		w.add("(message ILIKE ? OR actor ILIKE ?)", "%"+v+"%", "%"+v+"%")
	}
	if c := q.Get("cursor"); c != "" {
		id, err := strconv.ParseInt(c, 10, 64)
		if err != nil {
			return nil, errStatus(http.StatusBadRequest, "invalid cursor")
		}
		w.add("id < ?", id)
	}
	limit := limitParam(q, 100, 500)
	rows, err := s.db.Query(r.Context(), `SELECT id, time, level, type, lane_id, actor, message FROM events`+w.sql()+
		fmt.Sprintf(` ORDER BY id DESC LIMIT %d`, limit+1), w.args...)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Event, error) {
		var e Event
		err := row.Scan(&e.ID, &e.Time, &e.Level, &e.Type, &e.LaneID, &e.Actor, &e.Message)
		return e, err
	})
	if err != nil {
		return nil, err
	}
	out := page[Event]{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		out.NextCursor = strconv.FormatInt(out.Items[limit-1].ID, 10)
	}
	if out.Items == nil {
		out.Items = []Event{}
	}
	return out, nil
}

// readBody reads a JSON body once so it can be decoded twice.
func readBody(r *http.Request) ([]byte, error) {
	b, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		return nil, errStatus(http.StatusBadRequest, "body too large")
	}
	return b, nil
}

func unmarshalBoth(b []byte, a, c any) error {
	if err := json.Unmarshal(b, a); err != nil {
		return errStatus(http.StatusBadRequest, "invalid JSON body")
	}
	if err := json.Unmarshal(b, c); err != nil {
		return errStatus(http.StatusBadRequest, "invalid JSON body")
	}
	return nil
}
