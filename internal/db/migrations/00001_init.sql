-- +goose Up

CREATE TABLE admins (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'viewer')),
    disabled      BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    admin_id   BIGINT NOT NULL REFERENCES admins ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    ip         TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_admin ON sessions (admin_id);

CREATE TABLE api_tokens (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    prefix       TEXT NOT NULL,
    scopes       TEXT[] NOT NULL,
    created_by   BIGINT REFERENCES admins ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

-- Private keys are encrypted with LANEPOOL_SECRET (AES-GCM).
CREATE TABLE provider_keys (
    id              BIGSERIAL PRIMARY KEY,
    provider        TEXT NOT NULL DEFAULT 'surfshark',
    label           TEXT NOT NULL DEFAULT '',
    private_key_enc BYTEA NOT NULL,
    public_key      TEXT NOT NULL UNIQUE,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE wireguard_configs (
    id                BIGSERIAL PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,
    country_code      TEXT NOT NULL DEFAULT '',
    city              TEXT NOT NULL DEFAULT '',
    endpoint          TEXT NOT NULL,
    peer_key          TEXT NOT NULL,
    preshared_key_enc BYTEA,
    private_key_enc   BYTEA NOT NULL,
    addresses         TEXT[] NOT NULL,
    dns               TEXT[] NOT NULL DEFAULT '{}',
    mtu               INT NOT NULL DEFAULT 0,
    enabled           BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Lanes are derived from providers; admins can disable individual lanes.
CREATE TABLE lanes (
    id           TEXT PRIMARY KEY,
    provider     TEXT NOT NULL,
    name         TEXT NOT NULL,
    country      TEXT NOT NULL DEFAULT '',
    country_code TEXT NOT NULL DEFAULT '',
    city         TEXT NOT NULL DEFAULT '',
    virtual      BOOLEAN NOT NULL DEFAULT false,
    endpoint     TEXT NOT NULL DEFAULT '',
    peer_key     TEXT NOT NULL DEFAULT '',
    position     INT NOT NULL DEFAULT 0,
    enabled      BOOLEAN NOT NULL DEFAULT true,
    active       BOOLEAN NOT NULL DEFAULT true, -- false when no longer selected by the provider config
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE proxy_users (
    id               BIGSERIAL PRIMARY KEY,
    username         TEXT NOT NULL,
    password_hash    TEXT NOT NULL,
    enabled          BOOLEAN NOT NULL DEFAULT true,
    note             TEXT NOT NULL DEFAULT '',
    expires_at       TIMESTAMPTZ,
    allowed_countries TEXT[] NOT NULL DEFAULT '{}',
    allowed_lanes    TEXT[] NOT NULL DEFAULT '{}',
    allow_domains    TEXT[] NOT NULL DEFAULT '{}',
    deny_domains     TEXT[] NOT NULL DEFAULT '{}',
    allowed_cidrs    TEXT[] NOT NULL DEFAULT '{}',
    sticky_minutes   INT NOT NULL DEFAULT 0,
    max_connections  INT NOT NULL DEFAULT 0,
    conn_per_second  DOUBLE PRECISION NOT NULL DEFAULT 0,
    quota_bytes      BIGINT NOT NULL DEFAULT 0,
    used_bytes       BIGINT NOT NULL DEFAULT 0,
    period_start     TIMESTAMPTZ NOT NULL DEFAULT date_trunc('month', now()),
    log_destinations BOOLEAN NOT NULL DEFAULT false,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX proxy_users_username ON proxy_users (lower(username));

-- Hourly rollups; kept long and used for analytics and quotas.
CREATE TABLE usage_hourly (
    hour        TIMESTAMPTZ NOT NULL,
    user_id     BIGINT NOT NULL,
    lane_id     TEXT NOT NULL,
    bytes_up    BIGINT NOT NULL DEFAULT 0,
    bytes_down  BIGINT NOT NULL DEFAULT 0,
    connections BIGINT NOT NULL DEFAULT 0,
    failures    BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (hour, user_id, lane_id)
);
CREATE INDEX usage_hourly_user ON usage_hourly (user_id, hour);

-- One row per proxy connection. Partitioned by day so retention drops whole partitions.
CREATE TABLE connection_logs (
    id         TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at   TIMESTAMPTZ NOT NULL,
    user_id    BIGINT,
    username   TEXT NOT NULL DEFAULT '',
    client_ip  TEXT NOT NULL DEFAULT '',
    target     TEXT NOT NULL DEFAULT '',
    lane_id    TEXT NOT NULL DEFAULT '',
    exit_ip    TEXT NOT NULL DEFAULT '',
    protocol   TEXT NOT NULL DEFAULT '',
    bytes_up   BIGINT NOT NULL DEFAULT 0,
    bytes_down BIGINT NOT NULL DEFAULT 0,
    result     TEXT NOT NULL,
    error      TEXT NOT NULL DEFAULT ''
) PARTITION BY RANGE (started_at);
CREATE TABLE connection_logs_default PARTITION OF connection_logs DEFAULT;
CREATE INDEX connection_logs_started ON connection_logs (started_at DESC);
CREATE INDEX connection_logs_user ON connection_logs (user_id, started_at DESC);

CREATE TABLE events (
    id      BIGSERIAL PRIMARY KEY,
    time    TIMESTAMPTZ NOT NULL DEFAULT now(),
    level   TEXT NOT NULL DEFAULT 'info',
    type    TEXT NOT NULL,
    lane_id TEXT NOT NULL DEFAULT '',
    actor   TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX events_time ON events (time DESC);
CREATE INDEX events_type ON events (type, time DESC);

CREATE TABLE burned_ips (
    id         BIGSERIAL PRIMARY KEY,
    domain     TEXT NOT NULL,
    lane_id    TEXT NOT NULL,
    exit_ip    TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL CHECK (source IN ('manual', 'auto', 'api')),
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX burned_ips_expires ON burned_ips (expires_at);

CREATE TABLE alert_channels (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('telegram', 'discord', 'slack', 'webhook')),
    config_enc BYTEA NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE alert_rules (
    id               BIGSERIAL PRIMARY KEY,
    name             TEXT NOT NULL,
    event            TEXT NOT NULL,
    threshold        INT NOT NULL DEFAULT 0,
    cooldown_minutes INT NOT NULL DEFAULT 30,
    channel_ids      BIGINT[] NOT NULL DEFAULT '{}',
    enabled          BOOLEAN NOT NULL DEFAULT true,
    last_fired_at    TIMESTAMPTZ
);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE settings, alert_rules, alert_channels, burned_ips, events, connection_logs,
    usage_hourly, proxy_users, lanes, wireguard_configs, provider_keys, api_tokens, sessions, admins;
