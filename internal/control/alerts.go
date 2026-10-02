package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

// Alert rule events.
var alertEvents = map[string]string{
	"lanes_below":    "fewer than N lanes are up",
	"lane_down":      "a lane stopped working",
	"breaker_open":   "new lane connections were paused after repeated failures",
	"key_failing":    "N or more lanes using one key can't connect",
	"quota_reached":  "a proxy user used up their quota",
	"engine_offline": "the engine hasn't reported for N seconds",
	"auth_failures":  "N or more failed proxy logins in 10 minutes",
}

type channelConfig struct {
	BotToken   string `json:"botToken,omitempty"`
	ChatID     string `json:"chatId,omitempty"`
	WebhookURL string `json:"webhookUrl,omitempty"`
}

type alertChannel struct {
	ID      int64         `json:"id"`
	Name    string        `json:"name"`
	Kind    string        `json:"kind"`
	Enabled bool          `json:"enabled"`
	Config  channelConfig `json:"config"`
}

func mask(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 4 {
		return "••••"
	}
	return "••••" + v[len(v)-4:]
}

func (c alertChannel) masked() alertChannel {
	c.Config.BotToken = mask(c.Config.BotToken)
	if c.Config.WebhookURL != "" {
		if u, err := url.Parse(c.Config.WebhookURL); err == nil {
			c.Config.WebhookURL = u.Scheme + "://" + u.Host + "/" + mask(u.Path)
		}
	}
	return c
}

func (s *Server) loadChannels(ctx context.Context, onlyEnabled bool) ([]alertChannel, error) {
	q := `SELECT id, name, kind, enabled, config_enc FROM alert_channels`
	if onlyEnabled {
		q += ` WHERE enabled`
	}
	rows, err := s.db.Query(ctx, q+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alertChannel
	for rows.Next() {
		var c alertChannel
		var enc []byte
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.Enabled, &enc); err != nil {
			return nil, err
		}
		if plain, err := s.box.open(enc); err == nil {
			json.Unmarshal(plain, &c.Config)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Server) listChannels(_ http.ResponseWriter, r *http.Request) (any, error) {
	chs, err := s.loadChannels(r.Context(), false)
	if err != nil {
		return nil, err
	}
	out := []alertChannel{}
	for _, c := range chs {
		out = append(out, c.masked())
	}
	return out, nil
}

func validateChannel(c alertChannel) map[string]string {
	f := map[string]string{}
	if strings.TrimSpace(c.Name) == "" {
		f["name"] = "required"
	}
	switch c.Kind {
	case "telegram":
		if c.Config.BotToken == "" {
			f["config.botToken"] = "required"
		}
		if c.Config.ChatID == "" {
			f["config.chatId"] = "required"
		}
	case "discord", "slack", "webhook":
		if u, err := url.Parse(c.Config.WebhookURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			f["config.webhookUrl"] = "enter the webhook URL"
		}
	default:
		f["kind"] = "telegram, discord, slack or webhook"
	}
	return f
}

func (s *Server) createChannel(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in alertChannel
	in.Enabled = true
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if f := validateChannel(in); len(f) > 0 {
		return nil, errFields(f)
	}
	cfg, _ := json.Marshal(in.Config)
	if err := s.db.QueryRow(r.Context(), `INSERT INTO alert_channels (name, kind, config_enc, enabled) VALUES ($1, $2, $3, $4) RETURNING id`,
		strings.TrimSpace(in.Name), in.Kind, s.box.seal(cfg), in.Enabled).Scan(&in.ID); err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.alert_channel", "added alert channel "+in.Name)
	return in.masked(), nil
}

func (s *Server) channelByID(ctx context.Context, id int64) (alertChannel, error) {
	chs, err := s.loadChannels(ctx, false)
	if err != nil {
		return alertChannel{}, err
	}
	for _, c := range chs {
		if c.ID == id {
			return c, nil
		}
	}
	return alertChannel{}, errNotFound
}

func (s *Server) patchChannel(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	cur, err := s.channelByID(r.Context(), id)
	if err != nil {
		return nil, err
	}
	var in struct {
		Name    *string        `json:"name"`
		Enabled *bool          `json:"enabled"`
		Config  *channelConfig `json:"config"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.Name != nil {
		cur.Name = *in.Name
	}
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	if in.Config != nil {
		// Masked values coming back unchanged keep the stored secret.
		if in.Config.BotToken != "" && !strings.HasPrefix(in.Config.BotToken, "••••") {
			cur.Config.BotToken = in.Config.BotToken
		}
		if in.Config.WebhookURL != "" && !strings.Contains(in.Config.WebhookURL, "••••") {
			cur.Config.WebhookURL = in.Config.WebhookURL
		}
		if in.Config.ChatID != "" {
			cur.Config.ChatID = in.Config.ChatID
		}
	}
	if f := validateChannel(cur); len(f) > 0 {
		return nil, errFields(f)
	}
	cfg, _ := json.Marshal(cur.Config)
	if _, err := s.db.Exec(r.Context(), `UPDATE alert_channels SET name = $2, enabled = $3, config_enc = $4 WHERE id = $1`,
		id, cur.Name, cur.Enabled, s.box.seal(cfg)); err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.alert_channel", "updated alert channel "+cur.Name)
	return cur.masked(), nil
}

func (s *Server) deleteChannel(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `DELETE FROM alert_channels WHERE id = $1 RETURNING name`, id).Scan(&name); err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.alert_channel", "deleted alert channel "+name)
	return nil, nil
}

func (s *Server) testChannel(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	c, err := s.channelByID(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if err := sendAlert(r.Context(), c, "lanepool test", "This is a test alert from lanepool. Alerts are working."); err != nil {
		return nil, errStatus(http.StatusFailedDependency, "sending failed: "+err.Error())
	}
	return nil, nil
}

var alertHTTP = &http.Client{Timeout: 10 * time.Second}

func sendAlert(ctx context.Context, c alertChannel, title, msg string) error {
	var target string
	var payload any
	switch c.Kind {
	case "telegram":
		target = "https://api.telegram.org/bot" + c.Config.BotToken + "/sendMessage"
		payload = map[string]string{"chat_id": c.Config.ChatID, "text": "⚠️ " + title + "\n" + msg}
	case "discord":
		target, payload = c.Config.WebhookURL, map[string]string{"content": "**" + title + "**\n" + msg}
	case "slack":
		target, payload = c.Config.WebhookURL, map[string]string{"text": "*" + title + "*\n" + msg}
	default:
		target, payload = c.Config.WebhookURL, map[string]any{"title": title, "message": msg, "time": time.Now().UTC()}
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := alertHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request failed")
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// --- rules ------------------------------------------------------------------------

type alertRule struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Event           string     `json:"event"`
	Threshold       int        `json:"threshold"`
	CooldownMinutes int        `json:"cooldownMinutes"`
	ChannelIDs      []int64    `json:"channelIds"`
	Enabled         bool       `json:"enabled"`
	LastFiredAt     *time.Time `json:"lastFiredAt,omitempty"`
}

const ruleCols = `id, name, event, threshold, cooldown_minutes, channel_ids, enabled, last_fired_at`

func scanRule(row pgx.Row) (alertRule, error) {
	var a alertRule
	err := row.Scan(&a.ID, &a.Name, &a.Event, &a.Threshold, &a.CooldownMinutes, &a.ChannelIDs, &a.Enabled, &a.LastFiredAt)
	return a, err
}

func (s *Server) loadRules(ctx context.Context) ([]alertRule, error) {
	rows, err := s.db.Query(ctx, `SELECT `+ruleCols+` FROM alert_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (alertRule, error) { return scanRule(row) })
	if out == nil {
		out = []alertRule{}
	}
	return out, err
}

func (s *Server) listRules(_ http.ResponseWriter, r *http.Request) (any, error) {
	return s.loadRules(r.Context())
}

func validateRule(a alertRule) map[string]string {
	f := map[string]string{}
	if strings.TrimSpace(a.Name) == "" {
		f["name"] = "required"
	}
	if _, ok := alertEvents[a.Event]; !ok {
		f["event"] = "unknown event"
	}
	if a.Threshold < 0 {
		f["threshold"] = "can't be negative"
	}
	if a.CooldownMinutes < 1 {
		f["cooldownMinutes"] = "at least 1 minute"
	}
	if len(a.ChannelIDs) == 0 {
		f["channelIds"] = "choose at least one channel"
	}
	return f
}

func (s *Server) createRule(_ http.ResponseWriter, r *http.Request) (any, error) {
	in := alertRule{Enabled: true, CooldownMinutes: 30}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if f := validateRule(in); len(f) > 0 {
		return nil, errFields(f)
	}
	a, err := scanRule(s.db.QueryRow(r.Context(), `INSERT INTO alert_rules (name, event, threshold, cooldown_minutes, channel_ids, enabled)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+ruleCols, strings.TrimSpace(in.Name), in.Event, in.Threshold, in.CooldownMinutes, in.ChannelIDs, in.Enabled))
	if err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.alert_rule", "added alert rule "+a.Name)
	return a, nil
}

func (s *Server) patchRule(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	cur, err := scanRule(s.db.QueryRow(r.Context(), `SELECT `+ruleCols+` FROM alert_rules WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	if err := decode(r, &cur); err != nil {
		return nil, err
	}
	cur.ID = id
	if f := validateRule(cur); len(f) > 0 {
		return nil, errFields(f)
	}
	a, err := scanRule(s.db.QueryRow(r.Context(), `UPDATE alert_rules SET name = $2, event = $3, threshold = $4,
		cooldown_minutes = $5, channel_ids = $6, enabled = $7 WHERE id = $1 RETURNING `+ruleCols,
		id, cur.Name, cur.Event, cur.Threshold, cur.CooldownMinutes, cur.ChannelIDs, cur.Enabled))
	if err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.alert_rule", "updated alert rule "+a.Name)
	return a, nil
}

func (s *Server) deleteRule(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `DELETE FROM alert_rules WHERE id = $1 RETURNING name`, id).Scan(&name); err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.alert_rule", "deleted alert rule "+name)
	return nil, nil
}

// --- evaluation ---------------------------------------------------------------------

// alerter evaluates rules on each engine report and periodically.
type alerter struct {
	s     *Server
	mu    sync.Mutex
	fired map[string]time.Time // rule|subject -> last fired
}

type firing struct{ subject, title, msg string }

func (a *alerter) onReport(ctx context.Context, rep *protocol.Report) {
	go a.evaluate(context.WithoutCancel(ctx), rep)
}

// evaluate checks every enabled rule; rep may be nil for the periodic check.
func (a *alerter) evaluate(ctx context.Context, rep *protocol.Report) {
	s := a.s
	rules, err := s.loadRules(ctx)
	if err != nil || len(rules) == 0 {
		return
	}
	if rep == nil {
		s.mu.RLock()
		rep = s.report
		s.mu.RUnlock()
	}
	status := s.engineStatus()
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		var fire []firing
		switch rule.Event {
		case "engine_offline":
			limit := time.Duration(max(rule.Threshold, 60)) * time.Second
			if status.LastReportAt != nil && time.Since(*status.LastReportAt) > limit {
				fire = append(fire, firing{"", "Engine offline", fmt.Sprintf("No report from the engine for %s.", time.Since(*status.LastReportAt).Round(time.Second))})
			}
		case "lanes_below":
			if rep != nil {
				up := 0
				for _, l := range rep.Lanes {
					if l.Status == protocol.LaneUp {
						up++
					}
				}
				if up < rule.Threshold && len(rep.Lanes) > 0 {
					fire = append(fire, firing{"", "Few lanes up", fmt.Sprintf("Only %d of %d lanes are up (threshold %d).", up, len(rep.Lanes), rule.Threshold)})
				}
			}
		case "lane_down", "breaker_open":
			if rep != nil {
				want := protocol.EventLaneDown
				if rule.Event == "breaker_open" {
					want = protocol.EventBreakerOpen
				}
				for _, e := range rep.Events {
					if e.Type == want {
						fire = append(fire, firing{e.LaneID, strings.ReplaceAll(rule.Event, "_", " "), strings.TrimSpace(e.LaneID + " " + e.Message)})
					}
				}
			}
		case "key_failing":
			if rep != nil {
				perKey := map[int64]int{}
				for _, l := range rep.Lanes {
					if l.Status == protocol.LaneBackoff {
						perKey[l.KeyID]++
					}
				}
				for k, n := range perKey {
					if n >= max(rule.Threshold, 1) {
						fire = append(fire, firing{fmt.Sprint(k), "Key failing", fmt.Sprintf("%d lanes using key %d can't connect.", n, k)})
					}
				}
			}
		case "quota_reached":
			rows, err := s.db.Query(ctx, `SELECT username FROM proxy_users WHERE quota_bytes > 0 AND used_bytes >= quota_bytes AND enabled`)
			if err == nil {
				for rows.Next() {
					var u string
					if rows.Scan(&u) == nil {
						fire = append(fire, firing{u, "Quota reached", "Proxy user " + u + " has used their whole quota."})
					}
				}
				rows.Close()
			}
		case "auth_failures":
			var n int
			if s.db.QueryRow(ctx, `SELECT count(*) FROM connection_logs WHERE result = 'auth_failed' AND started_at > now() - interval '10 minutes'`).Scan(&n) == nil && n >= max(rule.Threshold, 1) {
				fire = append(fire, firing{"", "Failed proxy logins", fmt.Sprintf("%d failed proxy logins in the last 10 minutes.", n)})
			}
		}
		for _, f := range fire {
			a.send(ctx, rule, f)
		}
	}
}

func (a *alerter) send(ctx context.Context, rule alertRule, f firing) {
	key := fmt.Sprintf("%d|%s", rule.ID, f.subject)
	a.mu.Lock()
	if last, ok := a.fired[key]; ok && time.Since(last) < time.Duration(rule.CooldownMinutes)*time.Minute {
		a.mu.Unlock()
		return
	}
	a.fired[key] = time.Now()
	a.mu.Unlock()

	chs, err := a.s.loadChannels(ctx, true)
	if err != nil {
		return
	}
	want := map[int64]bool{}
	for _, id := range rule.ChannelIDs {
		want[id] = true
	}
	for _, c := range chs {
		if want[c.ID] {
			if err := sendAlert(ctx, c, "lanepool: "+f.title, f.msg); err != nil {
				a.s.log.Warn("alert delivery failed", "channel", c.Name, "err", err)
			}
		}
	}
	a.s.db.Exec(ctx, `UPDATE alert_rules SET last_fired_at = now() WHERE id = $1`, rule.ID)
	a.s.addEvent(ctx, "warn", "alert_fired", "", "alerts", rule.Name+": "+f.msg)
}
