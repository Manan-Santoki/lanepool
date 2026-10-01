package control

import (
	"context"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Manan-Santoki/lanepool/internal/auth"
	"github.com/Manan-Santoki/lanepool/internal/engine/gateway"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
)

// ProxyUser is a proxy account as the dashboard sees it.
type ProxyUser struct {
	ID                int64      `json:"id"`
	Username          string     `json:"username"`
	Enabled           bool       `json:"enabled"`
	Note              string     `json:"note"`
	ExpiresAt         *time.Time `json:"expiresAt"`
	AllowedCountries  []string   `json:"allowedCountries"`
	AllowedLanes      []string   `json:"allowedLanes"`
	AllowDomains      []string   `json:"allowDomains"`
	DenyDomains       []string   `json:"denyDomains"`
	AllowedCIDRs      []string   `json:"allowedCidrs"`
	StickyMinutes     int        `json:"stickyMinutes"`
	MaxConnections    int        `json:"maxConnections"`
	ConnPerSecond     float64    `json:"connPerSecond"`
	QuotaBytes        int64      `json:"quotaBytes"`
	UsedBytes         int64      `json:"usedBytes"`
	PeriodStart       time.Time  `json:"periodStart"`
	LogDestinations   bool       `json:"logDestinations"`
	ActiveConnections int        `json:"activeConnections"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	LastSeenAt        *time.Time `json:"lastSeenAt,omitempty"`
	passwordHash      string
}

const userCols = `id, username, enabled, note, expires_at, allowed_countries, allowed_lanes, allow_domains,
	deny_domains, allowed_cidrs, sticky_minutes, max_connections, conn_per_second, quota_bytes, used_bytes,
	period_start, log_destinations, created_at, updated_at, last_seen_at, password_hash`

func scanUser(row pgx.Row) (*ProxyUser, error) {
	var u ProxyUser
	err := row.Scan(&u.ID, &u.Username, &u.Enabled, &u.Note, &u.ExpiresAt, &u.AllowedCountries, &u.AllowedLanes,
		&u.AllowDomains, &u.DenyDomains, &u.AllowedCIDRs, &u.StickyMinutes, &u.MaxConnections, &u.ConnPerSecond,
		&u.QuotaBytes, &u.UsedBytes, &u.PeriodStart, &u.LogDestinations, &u.CreatedAt, &u.UpdatedAt, &u.LastSeenAt,
		&u.passwordHash)
	return &u, err
}

func (s *Server) loadUsers(ctx context.Context) ([]*ProxyUser, error) {
	rows, err := s.db.Query(ctx, `SELECT `+userCols+` FROM proxy_users ORDER BY lower(username)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (*ProxyUser, error) { return scanUser(row) })
}

// activeByUser counts live connections per user from the latest engine report.
func (s *Server) activeByUser(ctx context.Context) map[int64]int {
	out := map[int64]int{}
	var live []protocol.LiveConn
	if err := s.engine.get(ctx, "/v1/connections", &live); err == nil {
		for _, c := range live {
			out[c.UserID]++
		}
	}
	return out
}

func (s *Server) listUsers(_ http.ResponseWriter, r *http.Request) (any, error) {
	users, err := s.loadUsers(r.Context())
	if err != nil {
		return nil, err
	}
	active := s.activeByUser(r.Context())
	for _, u := range users {
		u.ActiveConnections = active[u.ID]
	}
	return users, nil
}

func (s *Server) getUser(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	u, err := scanUser(s.db.QueryRow(r.Context(), `SELECT `+userCols+` FROM proxy_users WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	u.ActiveConnections = s.activeByUser(r.Context())[u.ID]
	return u, nil
}

type userInput struct {
	Username         *string    `json:"username"`
	Password         *string    `json:"password"`
	Enabled          *bool      `json:"enabled"`
	Note             *string    `json:"note"`
	ExpiresAt        *time.Time `json:"expiresAt"`
	ClearExpiry      bool       `json:"-"`
	AllowedCountries *[]string  `json:"allowedCountries"`
	AllowedLanes     *[]string  `json:"allowedLanes"`
	AllowDomains     *[]string  `json:"allowDomains"`
	DenyDomains      *[]string  `json:"denyDomains"`
	AllowedCIDRs     *[]string  `json:"allowedCidrs"`
	StickyMinutes    *int       `json:"stickyMinutes"`
	MaxConnections   *int       `json:"maxConnections"`
	ConnPerSecond    *float64   `json:"connPerSecond"`
	QuotaBytes       *int64     `json:"quotaBytes"`
	LogDestinations  *bool      `json:"logDestinations"`
}

// Usernames may contain hyphens, but not the parameter keywords that the
// gateway parses out of usernames.
var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{1,63}$`)
var domainRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func (in *userInput) normalise() {
	clean := func(p *[]string, lower bool) {
		if p == nil {
			return
		}
		out := []string{}
		seen := map[string]bool{}
		for _, v := range *p {
			v = strings.TrimSpace(v)
			if lower {
				v = strings.ToLower(v)
			}
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
		*p = out
	}
	clean(in.AllowedCountries, true)
	clean(in.AllowedLanes, false)
	clean(in.AllowDomains, true)
	clean(in.DenyDomains, true)
	clean(in.AllowedCIDRs, false)
	if in.Username != nil {
		u := strings.TrimSpace(*in.Username)
		in.Username = &u
	}
}

func (in *userInput) validate(creating bool) map[string]string {
	f := map[string]string{}
	if creating && in.Username == nil {
		f["username"] = "required"
	}
	if in.Username != nil {
		if !usernameRe.MatchString(*in.Username) {
			f["username"] = "2-64 letters, digits, dots, dashes or underscores"
		} else if p := gateway.ParseUsername(*in.Username); p.Username != *in.Username {
			f["username"] = "can't contain -country-, -lane-, -session- or -sessttl- (used for proxy options)"
		}
	}
	if in.Password != nil {
		if err := auth.ValidatePassword(*in.Password); err != nil {
			f["password"] = err.Error()
		}
	}
	if in.AllowedCountries != nil {
		for _, c := range *in.AllowedCountries {
			if len(c) != 2 {
				f["allowedCountries"] = "use 2-letter country codes"
			}
		}
	}
	for field, list := range map[string]*[]string{"allowDomains": in.AllowDomains, "denyDomains": in.DenyDomains} {
		if list == nil {
			continue
		}
		for _, d := range *list {
			if !domainRe.MatchString(d) {
				f[field] = "invalid domain " + d
			}
		}
	}
	if in.AllowedCIDRs != nil {
		for _, c := range *in.AllowedCIDRs {
			if _, err := netip.ParsePrefix(c); err != nil {
				if _, err := netip.ParseAddr(c); err != nil {
					f["allowedCidrs"] = "invalid IP or CIDR " + c
				}
			}
		}
	}
	nonNeg := func(name string, v *int) {
		if v != nil && *v < 0 {
			f[name] = "can't be negative"
		}
	}
	nonNeg("stickyMinutes", in.StickyMinutes)
	nonNeg("maxConnections", in.MaxConnections)
	if in.ConnPerSecond != nil && *in.ConnPerSecond < 0 {
		f["connPerSecond"] = "can't be negative"
	}
	if in.QuotaBytes != nil && *in.QuotaBytes < 0 {
		f["quotaBytes"] = "can't be negative"
	}
	return f
}

func (s *Server) createUser(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in userInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	in.normalise()
	if f := in.validate(true); len(f) > 0 {
		return nil, errFields(f)
	}
	var generated string
	if in.Password == nil || *in.Password == "" {
		generated = auth.RandomToken("", 18)
		in.Password = &generated
	}
	hash, err := auth.HashPassword(*in.Password)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	var id int64
	err = s.db.QueryRow(ctx, `INSERT INTO proxy_users (username, password_hash) VALUES ($1, $2) RETURNING id`, *in.Username, hash).Scan(&id)
	if isUniqueViolation(err) {
		return nil, errFields(map[string]string{"username": "this username is taken"})
	}
	if err != nil {
		return nil, err
	}
	in.Password = nil
	u, err := s.applyUserUpdate(ctx, id, in)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, who(r), "admin.user_created", "created proxy user "+u.Username)
	s.configChanged()
	resp := map[string]any{"user": u}
	if generated != "" {
		resp["password"] = generated
	}
	return resp, nil
}

func (s *Server) applyUserUpdate(ctx context.Context, id int64, in userInput) (*ProxyUser, error) {
	// expiresAt: JSON null clears it; absent leaves it. Handled via raw presence check by caller.
	return scanUser(s.db.QueryRow(ctx, `UPDATE proxy_users SET
		username = COALESCE($2, username),
		enabled = COALESCE($3, enabled),
		note = COALESCE($4, note),
		expires_at = CASE WHEN $5::bool THEN $6 ELSE COALESCE($6, expires_at) END,
		allowed_countries = COALESCE($7, allowed_countries),
		allowed_lanes = COALESCE($8, allowed_lanes),
		allow_domains = COALESCE($9, allow_domains),
		deny_domains = COALESCE($10, deny_domains),
		allowed_cidrs = COALESCE($11, allowed_cidrs),
		sticky_minutes = COALESCE($12, sticky_minutes),
		max_connections = COALESCE($13, max_connections),
		conn_per_second = COALESCE($14, conn_per_second),
		quota_bytes = COALESCE($15, quota_bytes),
		log_destinations = COALESCE($16, log_destinations),
		updated_at = now()
		WHERE id = $1 RETURNING `+userCols,
		id, in.Username, in.Enabled, in.Note, in.ClearExpiry, in.ExpiresAt, in.AllowedCountries, in.AllowedLanes,
		in.AllowDomains, in.DenyDomains, in.AllowedCIDRs, in.StickyMinutes, in.MaxConnections, in.ConnPerSecond,
		in.QuotaBytes, in.LogDestinations))
}

func (s *Server) updateUser(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	var in userInput
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	if err := unmarshalBoth(body, &raw, &in); err != nil {
		return nil, err
	}
	if v, ok := raw["expiresAt"]; ok && v == nil {
		in.ClearExpiry = true
	}
	in.normalise()
	if f := in.validate(false); len(f) > 0 {
		return nil, errFields(f)
	}
	ctx := r.Context()
	if in.Password != nil {
		hash, err := auth.HashPassword(*in.Password)
		if err != nil {
			return nil, err
		}
		if _, err := s.db.Exec(ctx, `UPDATE proxy_users SET password_hash = $2 WHERE id = $1`, id, hash); err != nil {
			return nil, err
		}
		in.Password = nil
	}
	u, err := s.applyUserUpdate(ctx, id, in)
	if isUniqueViolation(err) {
		return nil, errFields(map[string]string{"username": "this username is taken"})
	}
	if err != nil {
		return nil, err
	}
	if in.Enabled != nil && !*in.Enabled {
		s.engine.post(ctx, "/v1/kick", protocol.KickRequest{UserID: id}, nil)
	}
	s.audit(ctx, who(r), "admin.user_updated", "updated proxy user "+u.Username)
	s.configChanged()
	return u, nil
}

func (s *Server) resetUserPassword(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	var in struct {
		Password string `json:"password"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			return nil, err
		}
	}
	if in.Password == "" {
		in.Password = auth.RandomToken("", 18)
	} else if err := auth.ValidatePassword(in.Password); err != nil {
		return nil, errFields(map[string]string{"password": err.Error()})
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `UPDATE proxy_users SET password_hash = $2, updated_at = now() WHERE id = $1 RETURNING username`, id, hash).Scan(&name); err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.user_password", "reset the password of proxy user "+name)
	s.configChanged()
	return map[string]string{"password": in.Password}, nil
}

func (s *Server) resetUserUsage(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	u, err := scanUser(s.db.QueryRow(r.Context(), `UPDATE proxy_users SET used_bytes = 0, period_start = now(), updated_at = now()
		WHERE id = $1 RETURNING `+userCols, id))
	if err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.user_usage_reset", "reset usage of proxy user "+u.Username)
	s.configChanged()
	return u, nil
}

func (s *Server) deleteUser(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `DELETE FROM proxy_users WHERE id = $1 RETURNING username`, id).Scan(&name); err != nil {
		return nil, err
	}
	s.engine.post(r.Context(), "/v1/kick", protocol.KickRequest{UserID: id}, nil)
	s.audit(r.Context(), who(r), "admin.user_deleted", "deleted proxy user "+name)
	s.configChanged()
	return nil, nil
}

func (u *ProxyUser) spec() protocol.UserSpec {
	return protocol.UserSpec{
		ID: u.ID, Username: u.Username, PasswordHash: u.passwordHash, Enabled: u.Enabled, ExpiresAt: u.ExpiresAt,
		AllowedCountries: u.AllowedCountries, AllowedLanes: u.AllowedLanes, AllowDomains: u.AllowDomains,
		DenyDomains: u.DenyDomains, AllowedCIDRs: u.AllowedCIDRs, StickyMinutes: u.StickyMinutes,
		MaxConnections: u.MaxConnections, ConnPerSecond: u.ConnPerSecond, QuotaBytes: u.QuotaBytes,
		UsedBytes: u.UsedBytes, LogDestinations: u.LogDestinations,
	}
}
