package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

// AppSettings are control-plane settings.
type AppSettings struct {
	LogRetentionDays   int    `json:"logRetentionDays"`
	EventRetentionDays int    `json:"eventRetentionDays"`
	PublicProxyHost    string `json:"publicProxyHost"`
	PublicHTTPSPort    int    `json:"publicHttpsPort"`
	PublicHTTPPort     int    `json:"publicHttpPort"`
	QuotaPeriod        string `json:"quotaPeriod"` // monthly | never
}

func defaultAppSettings() AppSettings {
	return AppSettings{LogRetentionDays: 7, EventRetentionDays: 90, PublicHTTPSPort: 443, QuotaPeriod: "monthly"}
}

// SurfsharkSelection decides which Surfshark locations become lanes.
type SurfsharkSelection struct {
	Lanes            int      `json:"lanes"`
	Countries        []string `json:"countries"`
	ExcludeCountries []string `json:"excludeCountries"`
	Locations        []string `json:"locations"`        // pinned: always lanes
	ExcludeLocations []string `json:"excludeLocations"` // removed: never picked automatically
	IncludeVirtual   bool     `json:"includeVirtual"`
	// AllServers makes every server of the matching locations a candidate lane
	// (each location has many server IPs) and keeps Lanes of them connected.
	AllServers bool `json:"allServers"`
	// SpreadKeys spreads lanes across all keys. Off (the default), every lane
	// uses the first key, as gluetun does: Surfshark accepts one key on many
	// servers at once, while lanes spread over many keys stopped connecting
	// once about ten keys were in use at the same time.
	SpreadKeys bool `json:"spreadKeys"`
}

func defaultSelection() SurfsharkSelection {
	return SurfsharkSelection{Lanes: 0, Countries: []string{}, ExcludeCountries: []string{}, Locations: []string{},
		ExcludeLocations: []string{}, IncludeVirtual: true}
}

// getSetting loads a JSON setting into v (which holds the defaults beforehand).
func (s *Server) getSetting(ctx context.Context, key string, v any) error {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (s *Server) putSetting(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, raw)
	return err
}

func (s *Server) engineSettings(ctx context.Context) (protocol.EngineSettings, error) {
	st := protocol.DefaultSettings()
	err := s.getSetting(ctx, "engine", &st)
	return st, err
}

func (s *Server) appSettings(ctx context.Context) (AppSettings, error) {
	st := defaultAppSettings()
	err := s.getSetting(ctx, "app", &st)
	return st, err
}

func (s *Server) selection(ctx context.Context) (SurfsharkSelection, error) {
	st := defaultSelection()
	err := s.getSetting(ctx, "surfshark.selection", &st)
	if st.ExcludeLocations == nil {
		st.ExcludeLocations = []string{}
	}
	return st, err
}

type settingsResponse struct {
	Engine protocol.EngineSettings `json:"engine"`
	App    AppSettings             `json:"app"`
}

func (s *Server) getSettings(_ http.ResponseWriter, r *http.Request) (any, error) {
	e, err := s.engineSettings(r.Context())
	if err != nil {
		return nil, err
	}
	a, err := s.appSettings(r.Context())
	return settingsResponse{Engine: e, App: a}, err
}

func (s *Server) putSettings(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Engine json.RawMessage `json:"engine"`
		App    json.RawMessage `json:"app"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	e, err := s.engineSettings(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.appSettings(ctx)
	if err != nil {
		return nil, err
	}
	// Partial updates: unmarshal over the current values.
	if len(in.Engine) > 0 {
		if err := json.Unmarshal(in.Engine, &e); err != nil {
			return nil, errStatus(http.StatusBadRequest, "invalid engine settings")
		}
	}
	if len(in.App) > 0 {
		if err := json.Unmarshal(in.App, &a); err != nil {
			return nil, errStatus(http.StatusBadRequest, "invalid app settings")
		}
	}
	f := validateEngineSettings(e)
	for k, v := range validateAppSettings(a) {
		f[k] = v
	}
	if len(f) > 0 {
		return nil, errFields(f)
	}
	if err := s.putSetting(ctx, "engine", e); err != nil {
		return nil, err
	}
	if err := s.putSetting(ctx, "app", a); err != nil {
		return nil, err
	}
	s.audit(ctx, who(r), "admin.settings", "updated settings")
	s.configChanged()
	return settingsResponse{Engine: e, App: a}, nil
}

func validateEngineSettings(e protocol.EngineSettings) map[string]string {
	f := map[string]string{}
	switch e.Strategy {
	case protocol.StrategyRoundRobin, protocol.StrategyRandom, protocol.StrategyLeastConnections, protocol.StrategyLowestLatency:
	default:
		f["engine.strategy"] = "unknown strategy"
	}
	check := func(name string, v, lo, hi int) {
		if v < lo || v > hi {
			f["engine."+name] = fmt.Sprintf("must be between %d and %d", lo, hi)
		}
	}
	check("laneStartDelay", e.LaneStartDelay, 0, 3600)
	check("maxConnecting", e.MaxConnecting, 1, 100)
	check("connectTimeout", e.ConnectTimeout, 10, 600)
	check("retryBackoff", e.RetryBackoff, 5, 86400)
	check("retryBackoffMax", e.RetryBackoffMax, e.RetryBackoff, 7*86400)
	check("breakerFailures", e.BreakerFailures, 0, 1000)
	check("breakerPause", e.BreakerPause, 0, 7*86400)
	check("handshakeMaxAge", e.HandshakeMaxAge, 130, 3600) // WireGuard rekeys every 120s
	check("ipCheckInterval", e.IPCheckInterval, 0, 86400)
	check("dialTimeout", e.DialTimeout, 1, 300)
	check("idleTimeout", e.IdleTimeout, 10, 86400)
	check("autoBurnFailures", e.AutoBurnFailures, 0, 100)
	check("autoBurnTtl", e.AutoBurnTTL, 60, 30*86400)
	if e.IPCheckURL != "" {
		if u, err := url.Parse(e.IPCheckURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			f["engine.ipCheckUrl"] = "must be an http(s) URL"
		}
	}
	return f
}

func validateAppSettings(a AppSettings) map[string]string {
	f := map[string]string{}
	if a.LogRetentionDays < 1 || a.LogRetentionDays > 365 {
		f["app.logRetentionDays"] = "must be between 1 and 365"
	}
	if a.EventRetentionDays < 1 || a.EventRetentionDays > 3650 {
		f["app.eventRetentionDays"] = "must be between 1 and 3650"
	}
	if a.PublicHTTPSPort < 0 || a.PublicHTTPSPort > 65535 {
		f["app.publicHttpsPort"] = "invalid port"
	}
	if a.PublicHTTPPort < 0 || a.PublicHTTPPort > 65535 {
		f["app.publicHttpPort"] = "invalid port"
	}
	if strings.ContainsAny(a.PublicProxyHost, "/: ") {
		f["app.publicProxyHost"] = "host name only, without scheme or port"
	}
	if a.QuotaPeriod != "monthly" && a.QuotaPeriod != "never" {
		f["app.quotaPeriod"] = "must be monthly or never"
	}
	return f
}

func (s *Server) setMaintenance(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Paused bool `json:"paused"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	e, err := s.engineSettings(ctx)
	if err != nil {
		return nil, err
	}
	e.Paused = in.Paused
	if err := s.putSetting(ctx, "engine", e); err != nil {
		return nil, err
	}
	msg := "turned maintenance mode off; new proxy connections are accepted"
	if in.Paused {
		msg = "turned maintenance mode on; new proxy connections are rejected"
	}
	s.audit(ctx, who(r), "admin.maintenance", msg)
	s.configChanged()
	return nil, nil
}
