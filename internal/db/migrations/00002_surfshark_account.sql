-- +goose Up

-- Keys registered at Surfshark through its account API are "managed": lanepool
-- created them, knows their Surfshark ID and expiry, and can rotate or delete them.
ALTER TABLE provider_keys
    ADD COLUMN remote_id  TEXT,
    ADD COLUMN expires_at TIMESTAMPTZ,
    ADD COLUMN managed    BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN rotated_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE provider_keys DROP COLUMN remote_id, DROP COLUMN expires_at, DROP COLUMN managed, DROP COLUMN rotated_at;
