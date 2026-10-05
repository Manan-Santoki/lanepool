// Command lanepool runs the control plane, the engine, or both.
//
//	lanepool control   dashboard, API and database
//	lanepool engine    VPN lanes and the proxy
//	lanepool all       both in one process (small setups)
//	lanepool admin create --email you@example.com
//	lanepool version
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Manan-Santoki/lanepool/internal/auth"
	"github.com/Manan-Santoki/lanepool/internal/control"
	"github.com/Manan-Santoki/lanepool/internal/db"
	"github.com/Manan-Santoki/lanepool/internal/engine"
	"github.com/Manan-Santoki/lanepool/internal/engine/lanes"
	"github.com/Manan-Santoki/lanepool/internal/lanesvc"
	"github.com/Manan-Santoki/lanepool/internal/protocol"
	"github.com/Manan-Santoki/lanepool/web"
)

var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel()}))
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "control":
		err = runControl(ctx, log)
	case "engine":
		err = runEngine(ctx, log)
	case "lanes":
		err = runLanes(ctx, log)
	case "all":
		err = runAll(ctx, log)
	case "admin":
		err = adminCommand(ctx, os.Args[2:])
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Error("exiting", "err", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: lanepool control | engine | lanes | all | admin create --email EMAIL | version")
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func logLevel() slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(env("LOG_LEVEL", "info"))); err != nil {
		return slog.LevelInfo
	}
	return l
}

func controlConfig() control.Config {
	cfg := control.Config{
		Listen:             env("LISTEN", ":8000"),
		DatabaseURL:        env("DATABASE_URL", "postgres://lanepool:lanepool@localhost:5432/lanepool?sslmode=disable"),
		Secret:             os.Getenv("LANEPOOL_SECRET"),
		EngineURL:          env("ENGINE_URL", "http://localhost:9090"),
		EngineToken:        os.Getenv("ENGINE_TOKEN"),
		CookieSecure:       env("COOKIE_SECURE", "false") == "true",
		SurfsharkAPI:       os.Getenv("SURFSHARK_API"),
		SurfsharkUserAgent: os.Getenv("SURFSHARK_USER_AGENT"),
		AdminEmail:         os.Getenv("ADMIN_EMAIL"),
		AdminPassword:      os.Getenv("ADMIN_PASSWORD"),
		Import:             v1Import(),
	}
	if dist, err := fs.Sub(web.Dist, "dist"); err == nil {
		if _, err := fs.Stat(dist, "index.html"); err == nil {
			cfg.Web = dist
		}
	}
	return cfg
}

// v1Import reads lanepool v1's environment variables for a one-time import.
func v1Import() control.V1Import {
	var im control.V1Import
	for _, k := range strings.Split(os.Getenv("SURFSHARK_PRIVATE_KEYS")+","+os.Getenv("SURFSHARK_PRIVATE_KEY"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			im.SurfsharkKeys = append(im.SurfsharkKeys, k)
		}
	}
	im.ProxyUser, im.ProxyPass = os.Getenv("PROXY_USER"), os.Getenv("PROXY_PASS")
	list := func(k string) []string {
		out := []string{}
		for _, v := range strings.Split(os.Getenv(k), ",") {
			if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	if n, err := strconv.Atoi(os.Getenv("LANES")); err == nil {
		im.Selection = &control.SurfsharkSelection{Lanes: n, Countries: list("COUNTRIES"), ExcludeCountries: list("EXCLUDE_COUNTRIES"),
			Locations: list("SURFSHARK_LOCATIONS"), IncludeVirtual: env("INCLUDE_VIRTUAL", "true") == "true"}
	}
	im.Engine = map[string]int{}
	for envName, field := range map[string]string{"LANE_START_DELAY": "laneStartDelay", "MAX_CONNECTING": "maxConnecting",
		"CONNECT_TIMEOUT": "connectTimeout", "RETRY_BACKOFF": "retryBackoff", "RETRY_BACKOFF_MAX": "retryBackoffMax",
		"BREAKER_FAILURES": "breakerFailures", "BREAKER_PAUSE": "breakerPause", "IP_CHECK_INTERVAL": "ipCheckInterval"} {
		if n, err := strconv.Atoi(os.Getenv(envName)); err == nil {
			im.Engine[field] = n
		}
	}
	switch os.Getenv("STRATEGY") {
	case "rr":
		im.Strategy = "round_robin"
	case "lha":
		im.Strategy = "lowest_latency"
	}
	im.PublicHost = os.Getenv("PUBLIC_PROXY_HOST")
	return im
}

func engineConfig() (engine.Config, error) {
	trusted, err := engine.ParseCIDRs(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return engine.Config{}, err
	}
	return engine.Config{
		NodeID:         env("NODE_ID", hostname()),
		ControlURL:     env("CONTROL_URL", "http://localhost:8000"),
		Token:          os.Getenv("ENGINE_TOKEN"),
		ProxyAddr:      env("PROXY_LISTEN", ":8080"),
		APIAddr:        env("ENGINE_LISTEN", ":9090"),
		TrustedProxies: trusted,
		LanesURL:       os.Getenv("LANES_URL"),
		LanesToken:     env("LANES_TOKEN", os.Getenv("ENGINE_TOKEN")),
		StateFile:      os.Getenv("LANES_STATE_FILE"),
	}, nil
}

func hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		return "engine"
	}
	return h
}

func runControl(ctx context.Context, log *slog.Logger) error {
	cfg := controlConfig()
	log.Info("starting control; connecting to the database", "version", version)
	srv, err := control.New(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer srv.Close()
	return srv.Run(ctx)
}

func runEngine(ctx context.Context, log *slog.Logger) error {
	cfg, err := engineConfig()
	if err != nil {
		return err
	}
	if cfg.Token == "" {
		return fmt.Errorf("ENGINE_TOKEN is required")
	}
	eng, err := engine.New(cfg, log)
	if err != nil {
		return err
	}
	return eng.Run(ctx)
}

// runLanes runs only the lanes (WireGuard tunnels) and serves them to an engine
// started with LANES_URL. Engine deploys then leave the tunnels connected.
func runLanes(ctx context.Context, log *slog.Logger) error {
	token := env("LANES_TOKEN", os.Getenv("ENGINE_TOKEN"))
	if token == "" {
		return fmt.Errorf("LANES_TOKEN (or ENGINE_TOKEN) is required")
	}
	m := lanes.New(protocol.DefaultSettings(), nil)
	var wg sync.WaitGroup
	if path := os.Getenv("LANES_STATE_FILE"); path != "" {
		lanes.LoadKnownGood(m, path, log)
		wg.Add(1)
		go func() { defer wg.Done(); lanes.SaveKnownGood(ctx, m, path, 30*time.Second, log) }()
	}
	go m.Run(ctx, time.Second)
	srv := &http.Server{Addr: env("LANES_LISTEN", ":9191"), Handler: lanesvc.NewServer(m, token, log), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Info("lanes listening; waiting for an engine to send the configuration", "addr", srv.Addr, "version", version)
	err := srv.ListenAndServe()
	wg.Wait() // the last save of the known lanes
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// runAll runs control and engine in one process. ENGINE_TOKEN is generated when
// unset, since both sides live in the same process.
func runAll(ctx context.Context, log *slog.Logger) error {
	if os.Getenv("ENGINE_TOKEN") == "" {
		b := make([]byte, 24)
		rand.Read(b)
		os.Setenv("ENGINE_TOKEN", hex.EncodeToString(b))
	}
	ccfg := controlConfig()
	ecfg, err := engineConfig()
	if err != nil {
		return err
	}
	ccfg.EngineURL = "http://127.0.0.1" + portOf(ecfg.APIAddr)
	ecfg.ControlURL = "http://127.0.0.1" + portOf(ccfg.Listen)
	srv, err := control.New(ctx, ccfg, log)
	if err != nil {
		return err
	}
	defer srv.Close()
	errc := make(chan error, 2)
	go func() { errc <- srv.Run(ctx) }()
	eng, err := engine.New(ecfg, log)
	if err != nil {
		return err
	}
	go func() { errc <- eng.Run(ctx) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		time.Sleep(500 * time.Millisecond) // let both flush
		return nil
	}
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ":" + addr
}

// adminCommand: lanepool admin create --email you@example.com [--password ...] [--role admin]
func adminCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return fmt.Errorf("usage: lanepool admin create --email EMAIL [--password PASSWORD] [--role admin|viewer]")
	}
	fl := flag.NewFlagSet("admin create", flag.ContinueOnError)
	email := fl.String("email", "", "email address")
	password := fl.String("password", "", "password (generated if empty)")
	role := fl.String("role", "admin", "admin or viewer")
	if err := fl.Parse(args[1:]); err != nil {
		return err
	}
	if *email == "" {
		return fmt.Errorf("--email is required")
	}
	generated := *password == ""
	if generated {
		*password = auth.RandomToken("", 15)
	}
	pool, err := db.Open(ctx, env("DATABASE_URL", "postgres://lanepool:lanepool@localhost:5432/lanepool?sslmode=disable"))
	if err != nil {
		return err
	}
	defer pool.Close()
	hash, err := auth.HashPassword(*password)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `INSERT INTO admins (email, name, password_hash, role) VALUES (lower($1), '', $2, $3)
		ON CONFLICT (email) DO UPDATE SET password_hash = EXCLUDED.password_hash, role = EXCLUDED.role, disabled = false`,
		*email, hash, *role)
	if err != nil {
		return err
	}
	fmt.Printf("admin %s is ready (role %s)\n", *email, *role)
	if generated {
		fmt.Printf("password: %s\n", *password)
	}
	return nil
}
