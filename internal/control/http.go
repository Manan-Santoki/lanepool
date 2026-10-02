package control

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
)

const sessionCookie = "lp_session"

// principal is whoever is calling the API.
type principal struct {
	admin  *Admin
	token  *apiTokenRow
	cookie bool
}

func (p *principal) name() string {
	switch {
	case p == nil:
		return ""
	case p.admin != nil:
		return p.admin.Email
	case p.token != nil:
		return "token:" + p.token.Name
	}
	return ""
}

// can reports whether the caller may perform an action needing scope
// ("read", "manage" or "rotate").
func (p *principal) can(scope string) bool {
	if p == nil {
		return false
	}
	if p.admin != nil {
		return scope == "read" || p.admin.Role == "admin"
	}
	// Token scopes: "manage" allows everything, "read" GET endpoints, "rotate"
	// the session/burn endpoints for apps.
	for _, s := range p.token.Scopes {
		if s == scope || s == "manage" {
			return true
		}
	}
	return false
}

type ctxKey struct{}

func who(r *http.Request) *principal {
	p, _ := r.Context().Value(ctxKey{}).(*principal)
	return p
}

// httpError is an error with a status code and optional field errors.
type httpError struct {
	status int
	msg    string
	fields map[string]string
}

func (e *httpError) Error() string { return e.msg }

func errStatus(status int, msg string) error { return &httpError{status: status, msg: msg} }

func errFields(fields map[string]string) error {
	return &httpError{status: http.StatusUnprocessableEntity, msg: "please fix the highlighted fields", fields: fields}
}

var errNotFound = errStatus(http.StatusNotFound, "not found")

// handler adapts functions that return (response, error).
type handler func(w http.ResponseWriter, r *http.Request) (any, error)

func (s *Server) h(fn handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := fn(w, r)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		switch v := v.(type) {
		case nil:
			w.WriteHeader(http.StatusNoContent)
		case status:
			w.WriteHeader(int(v))
		default:
			writeJSON(w, http.StatusOK, v)
		}
	}
}

// status is a response with no body.
type status int

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var he *httpError
	switch {
	case errors.As(err, &he):
		body := map[string]any{"error": he.msg}
		if he.fields != nil {
			body["fields"] = he.fields
		}
		writeJSON(w, he.status, body)
	case errors.Is(err, pgx.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	case isUniqueViolation(err):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already exists"})
	default:
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errStatus(http.StatusBadRequest, "invalid JSON body")
	}
	return nil
}

func idParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return 0, errNotFound
	}
	return id, nil
}

func clientIP(r *http.Request) string {
	// Behind Cloudflare the visitor's address is in CF-Connecting-IP.
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Router builds the HTTP routes.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"alive": true})
	})
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		dbOK := s.db.Ping(ctx) == nil
		engine := s.engineStatus()
		code := http.StatusOK
		if !dbOK || !engine.Connected {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, map[string]any{"database": dbOK, "engine": engine.Connected})
	})

	// Engine endpoints (shared token).
	r.Route("/internal/engine", func(r chi.Router) {
		r.Use(s.requireEngineToken)
		r.Get("/config", s.handleEngineConfig)
		r.Post("/report", s.handleEngineReport)
	})

	r.Route("/api", func(r chi.Router) {
		r.Use(s.authenticate)
		r.Get("/setup", s.h(s.getSetup))
		r.Post("/setup", s.h(s.postSetup))
		r.Post("/auth/login", s.h(s.login))
		r.Post("/auth/logout", s.h(s.logout))

		r.Group(func(r chi.Router) {
			r.Use(s.requireScope("read"))
			r.Get("/auth/me", s.h(s.me))
			r.Get("/overview", s.h(s.overview))
			r.Get("/stream", s.stream)
			r.Get("/lanes", s.h(s.listLanes))
			r.Get("/connections", s.h(s.liveConnections))
			r.Get("/users", s.h(s.listUsers))
			r.Get("/users/{id}", s.h(s.getUser))
			r.Get("/logs/connections", s.h(s.connectionLogs))
			r.Get("/logs/connections.csv", s.connectionLogsCSV)
			r.Get("/events", s.h(s.listEvents))
			r.Get("/analytics/traffic", s.h(s.traffic))
			r.Get("/analytics/top", s.h(s.top))
			r.Get("/burned", s.h(s.listBurned))
			r.Get("/providers/surfshark", s.h(s.getSurfshark))
			r.Get("/providers/surfshark/locations", s.h(s.surfsharkLocations))
			r.Get("/providers/surfshark/account", s.h(s.getSurfsharkAccount))
			r.Get("/providers/wireguard", s.h(s.listWireguard))
			r.Get("/alerts/channels", s.h(s.listChannels))
			r.Get("/alerts/rules", s.h(s.listRules))
			r.Get("/settings", s.h(s.getSettings))
			r.Get("/admins", s.h(s.listAdmins))
			r.Get("/tokens", s.h(s.listTokens))
		})

		r.Group(func(r chi.Router) {
			r.Use(s.requireScope("rotate"))
			r.Get("/lanes/random", s.h(s.randomLane))
			r.Post("/v1/rotate", s.h(s.rotateSession))
			r.Post("/v1/burn", s.h(s.apiBurn))
		})

		r.Group(func(r chi.Router) {
			r.Use(s.requireScope("manage"))
			r.Post("/auth/password", s.h(s.changeOwnPassword))
			r.Patch("/lanes/{lane}", s.h(s.patchLane))
			r.Post("/lanes/restart-all", s.h(s.restartAllLanes))
			r.Post("/lanes/add", s.h(s.addLanes))
			r.Delete("/lanes/{lane}", s.h(s.removeLane))
			r.Post("/lanes/{lane}/restart", s.h(s.restartLane))
			r.Post("/gateway/restart", s.h(s.restartGateway))
			r.Post("/gateway/maintenance", s.h(s.setMaintenance))
			r.Post("/connections/kick", s.h(s.kick))
			r.Post("/users", s.h(s.createUser))
			r.Patch("/users/{id}", s.h(s.updateUser))
			r.Post("/users/{id}/password", s.h(s.resetUserPassword))
			r.Post("/users/{id}/reset-usage", s.h(s.resetUserUsage))
			r.Delete("/users/{id}", s.h(s.deleteUser))
			r.Post("/burned", s.h(s.createBurned))
			r.Delete("/burned/{id}", s.h(s.deleteBurned))
			r.Post("/providers/surfshark/keys", s.h(s.addSurfsharkKey))
			r.Patch("/providers/surfshark/keys/{id}", s.h(s.patchSurfsharkKey))
			r.Delete("/providers/surfshark/keys/{id}", s.h(s.deleteSurfsharkKey))
			r.Put("/providers/surfshark/selection", s.h(s.putSurfsharkSelection))
			r.Put("/providers/surfshark/account", s.h(s.putSurfsharkAccount))
			r.Patch("/providers/surfshark/account", s.h(s.patchSurfsharkAccount))
			r.Delete("/providers/surfshark/account", s.h(s.deleteSurfsharkAccount))
			r.Post("/providers/surfshark/keys/generate", s.h(s.generateSurfsharkKeys))
			r.Post("/providers/surfshark/keys/{id}/rotate", s.h(s.rotateSurfsharkKey))
			r.Delete("/providers/surfshark/remote-keys/{remoteId}", s.h(s.deleteRemoteKey))
			r.Post("/providers/wireguard", s.h(s.createWireguard))
			r.Patch("/providers/wireguard/{id}", s.h(s.patchWireguard))
			r.Delete("/providers/wireguard/{id}", s.h(s.deleteWireguard))
			r.Post("/alerts/channels", s.h(s.createChannel))
			r.Patch("/alerts/channels/{id}", s.h(s.patchChannel))
			r.Delete("/alerts/channels/{id}", s.h(s.deleteChannel))
			r.Post("/alerts/channels/{id}/test", s.h(s.testChannel))
			r.Post("/alerts/rules", s.h(s.createRule))
			r.Patch("/alerts/rules/{id}", s.h(s.patchRule))
			r.Delete("/alerts/rules/{id}", s.h(s.deleteRule))
			r.Put("/settings", s.h(s.putSettings))
			r.Post("/admins", s.h(s.createAdmin))
			r.Patch("/admins/{id}", s.h(s.patchAdmin))
			r.Delete("/admins/{id}", s.h(s.deleteAdmin))
			r.Post("/tokens", s.h(s.createToken))
			r.Delete("/tokens/{id}", s.h(s.deleteToken))
		})
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		})
	})

	r.With(s.authenticate, s.requireScope("read")).Get("/metrics", s.metrics)
	r.Handle("/*", s.webHandler())
	return r
}

// authenticate resolves the session cookie or API token, if any.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var p *principal
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			t, err := s.tokenByValue(ctx, strings.TrimPrefix(h, "Bearer "))
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid API token"})
				return
			}
			p = &principal{token: t}
		} else if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			if a, err := s.sessionAdmin(ctx, c.Value); err == nil {
				p = &principal{admin: a, cookie: true}
			}
		}
		if p != nil && p.cookie && r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-site request blocked"})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, ctxKey{}, p)))
	})
}

// sameOrigin protects cookie-authenticated mutations from cross-site requests
// (on top of SameSite=Lax cookies).
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser clients
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	return strings.EqualFold(u.Host, host)
}

func (s *Server) requireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := who(r)
			if p == nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not logged in"})
				return
			}
			if !p.can(scope) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "you don't have permission to do this"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) requireEngineToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.cfg.EngineToken {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// webHandler serves the embedded dashboard with SPA fallback.
func (s *Server) webHandler() http.Handler {
	if s.cfg.Web == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte("<!doctype html><title>lanepool</title><p>The dashboard hasn't been built. Run <code>npm run build</code> in web/.</p>"))
		})
	}
	files := http.FileServer(http.FS(s.cfg.Web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := fs.Stat(s.cfg.Web, p); err == nil && !f.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(s.cfg.Web, "index.html")
		if err != nil {
			http.Error(w, "dashboard not built", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
