package control

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Manan-Santoki/lanepool/internal/auth"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/wg"
)

// jobs runs housekeeping: log partitions and retention, quota periods,
// periodic alert checks.
func (s *Server) jobs(ctx context.Context) {
	minute := time.NewTicker(time.Minute)
	defer minute.Stop()
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	s.housekeeping(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-minute.C:
			s.alerting.evaluate(ctx, nil)
		case <-hourly.C:
			s.housekeeping(ctx)
		}
	}
}

func (s *Server) housekeeping(ctx context.Context) {
	if err := s.ensurePartitions(ctx); err != nil {
		s.log.Warn("log partitions", "err", err)
	}
	if err := s.applyRetention(ctx); err != nil {
		s.log.Warn("retention", "err", err)
	}
	if err := s.resetQuotaPeriods(ctx); err != nil {
		s.log.Warn("quota periods", "err", err)
	}
}

func partitionName(day time.Time) string { return "connection_logs_" + day.Format("20060102") }

// ensurePartitions creates daily partitions from yesterday to three days ahead,
// so inserts never land in the default partition.
func (s *Server) ensurePartitions(ctx context.Context) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for d := -1; d <= 3; d++ {
		day := today.AddDate(0, 0, d)
		_, err := s.db.Exec(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF connection_logs
			FOR VALUES FROM ('%s') TO ('%s')`, partitionName(day), day.Format(time.RFC3339), day.AddDate(0, 0, 1).Format(time.RFC3339)))
		if err != nil && !strings.Contains(err.Error(), "would be violated by some row") {
			return err
		}
	}
	return nil
}

func (s *Server) applyRetention(ctx context.Context) error {
	app, err := s.appSettings(ctx)
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -app.LogRetentionDays)
	rows, err := s.db.Query(ctx, `SELECT c.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = 'connection_logs' AND c.relname ~ '^connection_logs_[0-9]{8}$'`)
	if err != nil {
		return err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, name := range names {
		day, err := time.Parse("20060102", strings.TrimPrefix(name, "connection_logs_"))
		if err == nil && day.Before(cutoff) {
			if _, err := s.db.Exec(ctx, `DROP TABLE IF EXISTS `+pgx.Identifier{name}.Sanitize()); err != nil {
				return err
			}
		}
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM connection_logs_default WHERE started_at < $1`, cutoff); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM events WHERE time < now() - make_interval(days => $1)`, app.EventRetentionDays); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM usage_hourly WHERE hour < now() - interval '400 days'`); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM burned_ips WHERE expires_at < now() - interval '7 days'`); err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}

func (s *Server) resetQuotaPeriods(ctx context.Context) error {
	app, err := s.appSettings(ctx)
	if err != nil || app.QuotaPeriod != "monthly" {
		return err
	}
	tag, err := s.db.Exec(ctx, `UPDATE proxy_users SET used_bytes = 0, period_start = date_trunc('month', now())
		WHERE period_start < date_trunc('month', now())`)
	if err == nil && tag.RowsAffected() > 0 {
		s.addEvent(ctx, "info", "quota_period", "", "system", fmt.Sprintf("new quota period: usage reset for %d user(s)", tag.RowsAffected()))
		s.configChanged()
	}
	return err
}

// --- metrics ------------------------------------------------------------------------

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	reg := prometheus.NewRegistry()
	lanes := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "lanepool_lanes", Help: "Lanes by status."}, []string{"status"})
	conns := prometheus.NewGauge(prometheus.GaugeOpts{Name: "lanepool_active_connections", Help: "Open proxy connections."})
	engineUp := prometheus.NewGauge(prometheus.GaugeOpts{Name: "lanepool_engine_connected", Help: "1 if the engine reported in the last 30s."})
	paused := prometheus.NewGauge(prometheus.GaugeOpts{Name: "lanepool_new_lanes_paused", Help: "1 while the circuit breaker blocks new lane connections."})
	userBytes := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "lanepool_user_used_bytes", Help: "Traffic used in the current quota period."}, []string{"user"})
	traffic := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "lanepool_traffic_bytes_24h", Help: "Traffic in the last 24 hours."}, []string{"direction"})
	reg.MustRegister(lanes, conns, engineUp, paused, userBytes, traffic)

	states, gw := s.latestLaneStates()
	counts := map[string]float64{}
	for _, st := range states {
		counts[st.Status]++
	}
	for _, st := range []string{protocol.LaneQueued, protocol.LaneConnecting, protocol.LaneUp, protocol.LaneDown, protocol.LaneBackoff, protocol.LaneDisabled} {
		lanes.WithLabelValues(st).Set(counts[st])
	}
	conns.Set(float64(gw.ActiveConnections))
	if s.engineStatus().Connected {
		engineUp.Set(1)
	}
	if gw.PausedUntil != nil {
		paused.Set(1)
	}
	if rows, err := s.db.Query(r.Context(), `SELECT username, used_bytes FROM proxy_users`); err == nil {
		for rows.Next() {
			var u string
			var b int64
			if rows.Scan(&u, &b) == nil {
				userBytes.WithLabelValues(u).Set(float64(b))
			}
		}
		rows.Close()
	}
	var up, down int64
	s.db.QueryRow(r.Context(), `SELECT COALESCE(sum(bytes_up),0)::bigint, COALESCE(sum(bytes_down),0)::bigint FROM usage_hourly WHERE hour >= now() - interval '24 hours'`).Scan(&up, &down)
	traffic.WithLabelValues("up").Set(float64(up))
	traffic.WithLabelValues("down").Set(float64(down))
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(w, r)
}

// --- first boot ---------------------------------------------------------------------

// V1Import carries settings from lanepool v1's environment variables, imported
// once into an empty database so existing deployments keep working.
type V1Import struct {
	SurfsharkKeys []string
	ProxyUser     string
	ProxyPass     string
	Selection     *SurfsharkSelection
	Engine        map[string]int // pacing variables, by EngineSettings JSON name
	Strategy      string
	PublicHost    string
}

func (s *Server) bootstrap(ctx context.Context) error {
	if n, err := s.adminCount(ctx); err != nil {
		return err
	} else if n == 0 && s.cfg.AdminEmail != "" && s.cfg.AdminPassword != "" {
		hash, err := auth.HashPassword(s.cfg.AdminPassword)
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(ctx, `INSERT INTO admins (email, name, password_hash, role) VALUES (lower($1), 'Admin', $2, 'admin')`,
			s.cfg.AdminEmail, hash); err != nil {
			return fmt.Errorf("create admin from ADMIN_EMAIL: %w", err)
		}
		s.log.Info("created admin account from ADMIN_EMAIL", "email", s.cfg.AdminEmail)
	}
	if err := s.ensurePartitions(ctx); err != nil {
		return err
	}
	return s.importV1(ctx)
}

func (s *Server) importV1(ctx context.Context) error {
	var done bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM settings WHERE key = 'v1_import')`).Scan(&done); err != nil || done {
		return err
	}
	im := s.cfg.Import
	imported := []string{}
	for i, k := range im.SurfsharkKeys {
		pub, err := publicKey(k)
		if err != nil || !wg.ValidKey(k) {
			s.log.Warn("skipping invalid key from SURFSHARK_PRIVATE_KEYS", "index", i+1)
			continue
		}
		if _, err := s.insertKey(ctx, k, pub, fmt.Sprintf("imported %d", i+1)); err != nil && !isUniqueViolation(err) {
			return err
		}
	}
	if len(im.SurfsharkKeys) > 0 {
		imported = append(imported, fmt.Sprintf("%d Surfshark key(s)", len(im.SurfsharkKeys)))
	}
	if im.ProxyUser != "" && im.ProxyPass != "" {
		hash, err := auth.HashPassword(im.ProxyPass)
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(ctx, `INSERT INTO proxy_users (username, password_hash, note) VALUES ($1, $2, 'imported from PROXY_USER')
			ON CONFLICT DO NOTHING`, im.ProxyUser, hash); err != nil {
			return err
		}
		imported = append(imported, "proxy user "+im.ProxyUser)
	}
	if im.Selection != nil {
		if err := s.putSetting(ctx, "surfshark.selection", im.Selection); err != nil {
			return err
		}
		imported = append(imported, "lane selection")
	}
	if len(im.Engine) > 0 || im.Strategy != "" {
		e := protocol.DefaultSettings()
		set := map[string]*int{"laneStartDelay": &e.LaneStartDelay, "maxConnecting": &e.MaxConnecting, "connectTimeout": &e.ConnectTimeout,
			"retryBackoff": &e.RetryBackoff, "retryBackoffMax": &e.RetryBackoffMax, "breakerFailures": &e.BreakerFailures,
			"breakerPause": &e.BreakerPause, "ipCheckInterval": &e.IPCheckInterval}
		for k, v := range im.Engine {
			if p, ok := set[k]; ok {
				*p = v
			}
		}
		if im.Strategy != "" {
			e.Strategy = im.Strategy
		}
		if f := validateEngineSettings(e); len(f) == 0 {
			if err := s.putSetting(ctx, "engine", e); err != nil {
				return err
			}
			imported = append(imported, "engine settings")
		}
	}
	if im.PublicHost != "" {
		a := defaultAppSettings()
		a.PublicProxyHost = im.PublicHost
		if err := s.putSetting(ctx, "app", a); err != nil {
			return err
		}
	}
	if err := s.putSetting(ctx, "v1_import", map[string]any{"at": time.Now(), "imported": imported}); err != nil {
		return err
	}
	if len(imported) > 0 {
		s.addEvent(ctx, "info", "v1_import", "", "system", "imported from environment: "+strings.Join(imported, ", "))
	}
	return nil
}
