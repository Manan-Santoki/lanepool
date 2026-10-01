package control

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Manan-Santoki/lanepool/internal/auth"
)

const sessionTTL = 7 * 24 * time.Hour

// Admin is a dashboard account.
type Admin struct {
	ID          int64      `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Role        string     `json:"role"`
	Disabled    bool       `json:"disabled"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastLoginAt *time.Time `json:"lastLoginAt,omitempty"`
}

type apiTokenRow struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

const adminCols = `id, email, name, role, disabled, created_at, last_login_at`

func scanAdmin(row pgx.Row) (*Admin, error) {
	var a Admin
	err := row.Scan(&a.ID, &a.Email, &a.Name, &a.Role, &a.Disabled, &a.CreatedAt, &a.LastLoginAt)
	return &a, err
}

func (s *Server) sessionAdmin(ctx context.Context, token string) (*Admin, error) {
	a, err := scanAdmin(s.db.QueryRow(ctx, `
		SELECT a.id, a.email, a.name, a.role, a.disabled, a.created_at, a.last_login_at
		FROM sessions s JOIN admins a ON a.id = s.admin_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND NOT a.disabled`, auth.HashToken(token)))
	if err != nil {
		return nil, err
	}
	// Sliding expiry, refreshed at most once a day.
	s.db.Exec(ctx, `UPDATE sessions SET expires_at = now() + $2::interval
		WHERE token_hash = $1 AND expires_at < now() + $2::interval - interval '1 day'`,
		auth.HashToken(token), sessionTTL.String())
	return a, nil
}

func (s *Server) tokenByValue(ctx context.Context, token string) (*apiTokenRow, error) {
	var t apiTokenRow
	err := s.db.QueryRow(ctx, `
		UPDATE api_tokens SET last_used_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())
		RETURNING id, name, prefix, scopes, created_at, last_used_at, expires_at`, auth.HashToken(token)).
		Scan(&t.ID, &t.Name, &t.Prefix, &t.Scopes, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Server) startSession(ctx context.Context, w http.ResponseWriter, r *http.Request, adminID int64) error {
	token := auth.RandomToken("", 32)
	_, err := s.db.Exec(ctx, `INSERT INTO sessions (token_hash, admin_id, expires_at, ip, user_agent)
		VALUES ($1, $2, now() + $3::interval, $4, $5)`,
		auth.HashToken(token), adminID, sessionTTL.String(), clientIP(r), truncate(r.UserAgent(), 200))
	if err != nil {
		return err
	}
	s.db.Exec(ctx, `UPDATE admins SET last_login_at = now() WHERE id = $1`, adminID)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL / time.Second),
	})
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// --- setup and login --------------------------------------------------------

func (s *Server) adminCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM admins`).Scan(&n)
	return n, err
}

func (s *Server) getSetup(_ http.ResponseWriter, r *http.Request) (any, error) {
	n, err := s.adminCount(r.Context())
	return map[string]bool{"needsSetup": n == 0}, err
}

type adminInput struct {
	Email    *string `json:"email"`
	Name     *string `json:"name"`
	Role     *string `json:"role"`
	Password *string `json:"password"`
	Disabled *bool   `json:"disabled"`
}

func validateAdmin(in adminInput, creating bool) map[string]string {
	f := map[string]string{}
	if creating || in.Email != nil {
		if in.Email == nil {
			f["email"] = "required"
		} else if _, err := mail.ParseAddress(*in.Email); err != nil || strings.ContainsAny(*in.Email, " <>") {
			f["email"] = "enter a valid email address"
		}
	}
	if creating || in.Password != nil {
		if in.Password == nil || len(*in.Password) < 10 || len(*in.Password) > 200 {
			f["password"] = "use at least 10 characters"
		}
	}
	if in.Role != nil && *in.Role != "admin" && *in.Role != "viewer" {
		f["role"] = "must be admin or viewer"
	}
	return f
}

func (s *Server) postSetup(w http.ResponseWriter, r *http.Request) (any, error) {
	var in adminInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if f := validateAdmin(in, true); len(f) > 0 {
		return nil, errFields(f)
	}
	ctx := r.Context()
	hash, err := auth.HashPassword(*in.Password)
	if err != nil {
		return nil, err
	}
	name := ""
	if in.Name != nil {
		name = *in.Name
	}
	// Only while no admin exists; the advisory lock prevents two concurrent setups.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4242)`)
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM admins`).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, errStatus(http.StatusConflict, "setup is already complete")
	}
	a, err := scanAdmin(tx.QueryRow(ctx, `INSERT INTO admins (email, name, password_hash, role)
		VALUES (lower($1), $2, $3, 'admin') RETURNING `+adminCols, strings.TrimSpace(*in.Email), name, hash))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.audit(ctx, &principal{admin: a}, "admin.setup", "created the first admin account")
	return map[string]any{"admin": a}, s.startSession(ctx, w, r, a.ID)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct{ Email, Password string }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	key := clientIP(r) + "|" + strings.ToLower(in.Email)
	if s.logins.blocked(key) || s.logins.blocked(clientIP(r)) {
		return nil, errStatus(http.StatusTooManyRequests, "too many failed attempts; try again in 15 minutes")
	}
	var hash string
	a, err := scanAdmin(s.db.QueryRow(ctx, `SELECT `+adminCols+` FROM admins WHERE email = lower($1)`, strings.TrimSpace(in.Email)))
	if err == nil {
		err = s.db.QueryRow(ctx, `SELECT password_hash FROM admins WHERE id = $1`, a.ID).Scan(&hash)
	}
	if err != nil || a.Disabled || !auth.VerifyPassword(hash, in.Password) {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		s.logins.fail(key)
		s.logins.fail(clientIP(r))
		return nil, errStatus(http.StatusUnauthorized, "wrong email or password")
	}
	s.logins.reset(key)
	s.audit(ctx, &principal{admin: a}, "admin.login", "logged in from "+clientIP(r))
	return map[string]any{"admin": a}, s.startSession(ctx, w, r, a.ID)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) (any, error) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.db.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash = $1`, auth.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode})
	return nil, nil
}

func (s *Server) me(_ http.ResponseWriter, r *http.Request) (any, error) {
	p := who(r)
	if p.admin == nil {
		return map[string]any{"token": p.token}, nil
	}
	return map[string]any{"admin": p.admin}, nil
}

func (s *Server) changeOwnPassword(_ http.ResponseWriter, r *http.Request) (any, error) {
	p := who(r)
	if p.admin == nil {
		return nil, errStatus(http.StatusForbidden, "only admins have passwords")
	}
	var in struct{ CurrentPassword, NewPassword string }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var hash string
	if err := s.db.QueryRow(ctx, `SELECT password_hash FROM admins WHERE id = $1`, p.admin.ID).Scan(&hash); err != nil {
		return nil, err
	}
	if !auth.VerifyPassword(hash, in.CurrentPassword) {
		return nil, errFields(map[string]string{"currentPassword": "incorrect password"})
	}
	if len(in.NewPassword) < 10 {
		return nil, errFields(map[string]string{"newPassword": "use at least 10 characters"})
	}
	newHash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(ctx, `UPDATE admins SET password_hash = $2 WHERE id = $1`, p.admin.ID, newHash); err != nil {
		return nil, err
	}
	// Sign out other sessions.
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.db.Exec(ctx, `DELETE FROM sessions WHERE admin_id = $1 AND token_hash <> $2`, p.admin.ID, auth.HashToken(c.Value))
	}
	s.audit(ctx, p, "admin.password", "changed their password")
	return nil, nil
}

// --- admins -----------------------------------------------------------------

func (s *Server) listAdmins(_ http.ResponseWriter, r *http.Request) (any, error) {
	rows, err := s.db.Query(r.Context(), `SELECT `+adminCols+` FROM admins ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (*Admin, error) { return scanAdmin(row) })
}

func (s *Server) createAdmin(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in adminInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.Role == nil {
		role := "viewer"
		in.Role = &role
	}
	if f := validateAdmin(in, true); len(f) > 0 {
		return nil, errFields(f)
	}
	hash, err := auth.HashPassword(*in.Password)
	if err != nil {
		return nil, err
	}
	name := ""
	if in.Name != nil {
		name = *in.Name
	}
	a, err := scanAdmin(s.db.QueryRow(r.Context(), `INSERT INTO admins (email, name, password_hash, role)
		VALUES (lower($1), $2, $3, $4) RETURNING `+adminCols, strings.TrimSpace(*in.Email), name, hash, *in.Role))
	if isUniqueViolation(err) {
		return nil, errFields(map[string]string{"email": "an admin with this email already exists"})
	}
	if err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.admin_created", "added "+a.Role+" "+a.Email)
	return a, nil
}

func (s *Server) patchAdmin(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	var in adminInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if f := validateAdmin(in, false); len(f) > 0 {
		return nil, errFields(f)
	}
	p := who(r)
	if p.admin != nil && p.admin.ID == id && ((in.Role != nil && *in.Role != "admin") || (in.Disabled != nil && *in.Disabled)) {
		return nil, errStatus(http.StatusBadRequest, "you can't demote or disable yourself")
	}
	ctx := r.Context()
	var hash *string
	if in.Password != nil {
		h, err := auth.HashPassword(*in.Password)
		if err != nil {
			return nil, err
		}
		hash = &h
	}
	a, err := scanAdmin(s.db.QueryRow(ctx, `UPDATE admins SET
		name = COALESCE($2, name), role = COALESCE($3, role), disabled = COALESCE($4, disabled),
		password_hash = COALESCE($5, password_hash), email = COALESCE(lower($6), email)
		WHERE id = $1 RETURNING `+adminCols, id, in.Name, in.Role, in.Disabled, hash, in.Email))
	if err != nil {
		return nil, err
	}
	if (in.Disabled != nil && *in.Disabled) || hash != nil {
		s.db.Exec(ctx, `DELETE FROM sessions WHERE admin_id = $1`, id)
	}
	s.audit(ctx, p, "admin.admin_updated", "updated admin "+a.Email)
	return a, nil
}

func (s *Server) deleteAdmin(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	p := who(r)
	if p.admin != nil && p.admin.ID == id {
		return nil, errStatus(http.StatusBadRequest, "you can't delete yourself")
	}
	var email string
	if err := s.db.QueryRow(r.Context(), `DELETE FROM admins WHERE id = $1 RETURNING email`, id).Scan(&email); err != nil {
		return nil, err
	}
	s.audit(r.Context(), p, "admin.admin_deleted", "deleted admin "+email)
	return nil, nil
}

// --- API tokens ---------------------------------------------------------------

func (s *Server) listTokens(_ http.ResponseWriter, r *http.Request) (any, error) {
	rows, err := s.db.Query(r.Context(), `SELECT id, name, prefix, scopes, created_at, last_used_at, expires_at
		FROM api_tokens WHERE revoked_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (apiTokenRow, error) {
		var t apiTokenRow
		err := row.Scan(&t.ID, &t.Name, &t.Prefix, &t.Scopes, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt)
		return t, err
	})
}

func (s *Server) createToken(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Name      string     `json:"name"`
		Scopes    []string   `json:"scopes"`
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	f := map[string]string{}
	if strings.TrimSpace(in.Name) == "" {
		f["name"] = "required"
	}
	if len(in.Scopes) == 0 {
		f["scopes"] = "choose at least one scope"
	}
	for _, sc := range in.Scopes {
		if sc != "read" && sc != "manage" && sc != "rotate" {
			f["scopes"] = "unknown scope " + sc
		}
	}
	if len(f) > 0 {
		return nil, errFields(f)
	}
	token := auth.RandomToken("lp_", 30)
	var createdBy *int64
	if a := who(r).admin; a != nil {
		createdBy = &a.ID
	}
	var t apiTokenRow
	err := s.db.QueryRow(r.Context(), `INSERT INTO api_tokens (name, token_hash, prefix, scopes, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, name, prefix, scopes, created_at, last_used_at, expires_at`,
		strings.TrimSpace(in.Name), auth.HashToken(token), token[:10], in.Scopes, createdBy, in.ExpiresAt).
		Scan(&t.ID, &t.Name, &t.Prefix, &t.Scopes, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt)
	if err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.token_created", "created API token "+t.Name)
	return map[string]any{"token": token, "apiToken": t}, nil
}

func (s *Server) deleteToken(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `UPDATE api_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL RETURNING name`, id).Scan(&name); err != nil {
		return nil, err
	}
	s.audit(r.Context(), who(r), "admin.token_revoked", "revoked API token "+name)
	return nil, nil
}

// audit records an admin action as an event.
func (s *Server) audit(ctx context.Context, p *principal, typ, msg string) {
	s.addEvent(ctx, "info", typ, "", p.name(), msg)
}
