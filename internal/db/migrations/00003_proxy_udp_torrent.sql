-- +goose Up
ALTER TABLE proxy_users
  ADD COLUMN udp_enabled BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN idle_timeout_seconds INT NOT NULL DEFAULT 0,
  ADD COLUMN log_mode TEXT NOT NULL DEFAULT 'full'
    CHECK (log_mode IN ('full', 'sampled', 'off'));

-- +goose Down
ALTER TABLE proxy_users
  DROP COLUMN udp_enabled,
  DROP COLUMN idle_timeout_seconds,
  DROP COLUMN log_mode;
