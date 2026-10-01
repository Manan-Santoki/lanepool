// Package control is lanepool's control plane: the admin API and dashboard,
// Postgres storage, and the engine's configuration and report endpoints.
package control

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Manan-Santoki/lanepool/internal/db"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/internal/providers/surfshark"
)

// Config configures the control plane.
type Config struct {
	Listen       string // e.g. :8000
	DatabaseURL  string
	Secret       string // encrypts provider keys and alert secrets at rest
	EngineURL    string // e.g. http://engine:9090
	EngineToken  string // shared with the engine
	CookieSecure bool   // set the Secure flag on the session cookie (HTTPS dashboards)
	SurfsharkAPI string // override for tests
	Web          fs.FS  // built dashboard (web/dist); nil serves a placeholder

	// First-boot bootstrap (only used while the database is empty).
	AdminEmail    string
	AdminPassword string
	Import        V1Import
}

// Server is the control plane.
type Server struct {
	cfg    Config
	db     *pgxpool.Pool
	log    *slog.Logger
	box    *secretBox
	engine *engineClient
	hub    *hub

	mu         sync.RWMutex
	report     *protocol.Report // latest engine report
	reportedAt time.Time

	servers      []surfshark.Server // cached Surfshark server list
	serversAt    time.Time
	serversErr   string
	serversMu    sync.Mutex
	syncRequired chan struct{}

	logins   *rateLimiter
	alerting *alerter
}

// New opens the database, migrates it and bootstraps first-run data.
func New(ctx context.Context, cfg Config, log *slog.Logger) (*Server, error) {
	if cfg.Secret == "" {
		return nil, errors.New("LANEPOOL_SECRET is required (it encrypts keys stored in the database)")
	}
	if cfg.EngineToken == "" {
		return nil, errors.New("ENGINE_TOKEN is required")
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg: cfg, db: pool, log: log, box: newSecretBox(cfg.Secret),
		engine:       &engineClient{url: cfg.EngineURL, token: cfg.EngineToken, http: &http.Client{Timeout: 10 * time.Second}},
		hub:          newHub(),
		syncRequired: make(chan struct{}, 1),
		logins:       newRateLimiter(10, 15*time.Minute),
	}
	s.alerting = &alerter{s: s, fired: map[string]time.Time{}}
	if err := s.bootstrap(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database pool.
func (s *Server) Close() { s.db.Close() }

// Run serves HTTP and runs background jobs until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{Addr: s.cfg.Listen, Handler: s.Router(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	s.log.Info("control listening", "addr", s.cfg.Listen)
	go s.jobs(ctx)
	go s.laneSyncLoop(ctx)

	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("control http: %w", err)
	}
}

// requestLaneSync asks the lane sync loop to rebuild lanes from the providers.
func (s *Server) requestLaneSync() {
	select {
	case s.syncRequired <- struct{}{}:
	default:
	}
}

// configChanged tells the engine to fetch its configuration now instead of at
// the next poll.
func (s *Server) configChanged() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.engine.post(ctx, "/v1/config/reload", nil, nil); err != nil {
			s.log.Debug("engine reload nudge failed", "err", err)
		}
	}()
}
