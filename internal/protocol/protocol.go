// Package protocol defines the messages exchanged between control and engine.
//
// The engine pulls its configuration from control (GET /internal/engine/config)
// and pushes state, connection records, usage and events back
// (POST /internal/engine/report). Control sends commands to the engine's own API.
// Engines never touch the database, so the same protocol works for remote exit
// nodes.
package protocol

import "time"

// Lane statuses.
const (
	LaneQueued     = "queued"     // waiting for its turn to connect
	LaneConnecting = "connecting" // tunnel started, no handshake yet
	LaneUp         = "up"         // recent handshake
	LaneDown       = "down"       // was up, handshakes stopped
	LaneBackoff    = "backoff"    // failed to connect; retrying later with the next key
	LaneDisabled   = "disabled"
)

// Lane selection strategies.
const (
	StrategyRoundRobin       = "round_robin"
	StrategyRandom           = "random"
	StrategyLeastConnections = "least_connections"
	StrategyLowestLatency    = "lowest_latency"
)

// Connection results.
const (
	ResultOK          = "ok"
	ResultAuthFailed  = "auth_failed"
	ResultDenied      = "denied"       // domain rule, client IP rule, disabled or expired user
	ResultQuota       = "quota"        // bandwidth quota used up
	ResultRateLimited = "rate_limited" // connection rate or concurrency limit
	ResultNoLane      = "no_lane"      // no healthy lane matches
	ResultDialFailed  = "dial_failed"  // target unreachable through the lane
	ResultKicked      = "kicked"
	ResultPaused      = "paused" // maintenance mode
	ResultBadRequest  = "bad_request"
)

// EngineConfig is everything an engine needs to run.
type EngineConfig struct {
	Version  string         `json:"version"`
	Settings EngineSettings `json:"settings"`
	Lanes    []LaneSpec     `json:"lanes"`
	Users    []UserSpec     `json:"users"`
	Burned   []BurnedIP     `json:"burned"`
}

// EngineSettings are the tunables, editable in the dashboard. Durations are seconds.
type EngineSettings struct {
	Strategy         string `json:"strategy"`
	Paused           bool   `json:"paused"` // reject new proxy connections
	LaneStartDelay   int    `json:"laneStartDelay"`
	MaxConnecting    int    `json:"maxConnecting"`
	ConnectTimeout   int    `json:"connectTimeout"`
	RetryBackoff     int    `json:"retryBackoff"`
	RetryBackoffMax  int    `json:"retryBackoffMax"`
	BreakerFailures  int    `json:"breakerFailures"`
	BreakerPause     int    `json:"breakerPause"`
	HandshakeMaxAge  int    `json:"handshakeMaxAge"`
	IPCheckURL       string `json:"ipCheckUrl"`
	IPCheckInterval  int    `json:"ipCheckInterval"`
	DialTimeout      int    `json:"dialTimeout"`
	IdleTimeout      int    `json:"idleTimeout"`
	AutoBurnFailures int    `json:"autoBurnFailures"` // 0 disables automatic burned-IP detection
	AutoBurnTTL      int    `json:"autoBurnTtl"`
}

// DefaultSettings are the defaults; pacing matches what v1 learned in production.
func DefaultSettings() EngineSettings {
	return EngineSettings{
		Strategy:         StrategyRoundRobin,
		LaneStartDelay:   10,
		MaxConnecting:    2,
		ConnectTimeout:   45,
		RetryBackoff:     300,
		RetryBackoffMax:  3600,
		BreakerFailures:  5,
		BreakerPause:     900,
		HandshakeMaxAge:  180,
		IPCheckURL:       "http://api.ipify.org/",
		IPCheckInterval:  600,
		DialTimeout:      15,
		IdleTimeout:      300,
		AutoBurnFailures: 3,
		AutoBurnTTL:      3600,
	}
}

// LaneSpec describes one lane: a WireGuard peer plus the keys it may use.
type LaneSpec struct {
	ID           string    `json:"id"` // stable, e.g. "surfshark:us-nyc"
	Name         string    `json:"name"`
	Provider     string    `json:"provider"` // "surfshark" or "wireguard"
	Country      string    `json:"country,omitempty"`
	CountryCode  string    `json:"countryCode,omitempty"`
	City         string    `json:"city,omitempty"`
	Virtual      bool      `json:"virtual,omitempty"`
	Enabled      bool      `json:"enabled"`
	Endpoint     string    `json:"endpoint"`
	PeerKey      string    `json:"peerKey"`
	PresharedKey string    `json:"presharedKey,omitempty"`
	Addresses    []string  `json:"addresses"`
	DNS          []string  `json:"dns,omitempty"`
	MTU          int       `json:"mtu,omitempty"`
	Keys         []LaneKey `json:"keys"` // tried in order; a failed lane moves to the next
}

// LaneKey is a private key a lane may connect with.
type LaneKey struct {
	ID         int64  `json:"id"`
	PrivateKey string `json:"privateKey"`
}

// UserSpec is a proxy user as the gateway enforces it.
type UserSpec struct {
	ID               int64      `json:"id"`
	Username         string     `json:"username"`
	PasswordHash     string     `json:"passwordHash"`
	Enabled          bool       `json:"enabled"`
	ExpiresAt        *time.Time `json:"expiresAt,omitempty"`
	AllowedCountries []string   `json:"allowedCountries,omitempty"`
	AllowedLanes     []string   `json:"allowedLanes,omitempty"`
	AllowDomains     []string   `json:"allowDomains,omitempty"` // empty = all
	DenyDomains      []string   `json:"denyDomains,omitempty"`
	AllowedCIDRs     []string   `json:"allowedCidrs,omitempty"` // client IPs; empty = any
	StickyMinutes    int        `json:"stickyMinutes"`          // 0 = new lane per connection
	MaxConnections   int        `json:"maxConnections"`         // concurrent; 0 = unlimited
	ConnPerSecond    float64    `json:"connPerSecond"`          // new connections; 0 = unlimited
	QuotaBytes       int64      `json:"quotaBytes"`             // per period; 0 = unlimited
	UsedBytes        int64      `json:"usedBytes"`              // used this period, as known to control
	LogDestinations  bool       `json:"logDestinations"`
}

// BurnedIP marks a lane as blocked by a domain until ExpiresAt.
type BurnedIP struct {
	Domain    string    `json:"domain"` // "example.com" also matches subdomains
	LaneID    string    `json:"laneId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Report is what an engine pushes to control every couple of seconds.
type Report struct {
	NodeID      string        `json:"nodeId"`
	StartedAt   time.Time     `json:"startedAt"`
	Lanes       []LaneState   `json:"lanes"`
	Gateway     GatewayState  `json:"gateway"`
	Connections []ConnRecord  `json:"connections,omitempty"`
	Usage       []UsageDelta  `json:"usage,omitempty"`
	Events      []Event       `json:"events,omitempty"`
	AutoBurned  []BurnedIP    `json:"autoBurned,omitempty"`
}

// LaneState is the runtime state of one lane.
type LaneState struct {
	ID                string     `json:"id"`
	Status            string     `json:"status"`
	KeyID             int64      `json:"keyId"`
	ExitIP            string     `json:"exitIp,omitempty"`
	LatencyMs         int        `json:"latencyMs,omitempty"`
	LastHandshake     *time.Time `json:"lastHandshake,omitempty"`
	ActiveConnections int        `json:"activeConnections"`
	RxBytes           uint64     `json:"rxBytes"`
	TxBytes           uint64     `json:"txBytes"`
	Restarts          int        `json:"restarts"`
	LastError         string     `json:"lastError,omitempty"`
	NextRetry         *time.Time `json:"nextRetry,omitempty"`
}

// GatewayState summarises the proxy listener.
type GatewayState struct {
	Listening         bool       `json:"listening"`
	ActiveConnections int        `json:"activeConnections"`
	PausedUntil       *time.Time `json:"pausedUntil,omitempty"` // circuit breaker
}

// ConnRecord is one finished (or rejected) proxy connection.
type ConnRecord struct {
	ID        string    `json:"id"`
	UserID    int64     `json:"userId,omitempty"`
	Username  string    `json:"username,omitempty"`
	ClientIP  string    `json:"clientIp"`
	Target    string    `json:"target,omitempty"` // host:port; empty if the user's destinations aren't logged
	LaneID    string    `json:"laneId,omitempty"`
	ExitIP    string    `json:"exitIp,omitempty"`
	Protocol  string    `json:"protocol"` // http, connect, socks5
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
	BytesUp   int64     `json:"bytesUp"`
	BytesDown int64     `json:"bytesDown"`
	Result    string    `json:"result"`
	Error     string    `json:"error,omitempty"`
}

// LiveConn is a connection that is still open.
type LiveConn struct {
	ID        string    `json:"id"`
	UserID    int64     `json:"userId"`
	Username  string    `json:"username"`
	ClientIP  string    `json:"clientIp"`
	Target    string    `json:"target,omitempty"`
	LaneID    string    `json:"laneId"`
	ExitIP    string    `json:"exitIp,omitempty"`
	Protocol  string    `json:"protocol"`
	StartedAt time.Time `json:"startedAt"`
	BytesUp   int64     `json:"bytesUp"`
	BytesDown int64     `json:"bytesDown"`
}

// UsageDelta is traffic since the previous report, per user and lane.
type UsageDelta struct {
	UserID      int64  `json:"userId"`
	LaneID      string `json:"laneId"`
	BytesUp     int64  `json:"bytesUp"`
	BytesDown   int64  `json:"bytesDown"`
	Connections int64  `json:"connections"`
	Failures    int64  `json:"failures"`
}

// Event types.
const (
	EventLaneUp         = "lane_up"
	EventLaneDown       = "lane_down"
	EventLaneBackoff    = "lane_backoff"
	EventKeyRotated     = "key_rotated"
	EventBreakerOpen    = "breaker_open"
	EventExitIPChanged  = "exit_ip_changed"
	EventAutoBurned     = "auto_burned"
	EventGatewayRestart = "gateway_restarted"
	EventEngineStarted  = "engine_started"
)

// Event is something that happened in the engine.
type Event struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"` // info, warn, error
	Type    string    `json:"type"`
	LaneID  string    `json:"laneId,omitempty"`
	Message string    `json:"message"`
}

// KickRequest closes live connections by ID or by user.
type KickRequest struct {
	IDs    []string `json:"ids,omitempty"`
	UserID int64    `json:"userId,omitempty"`
}
