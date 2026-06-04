-- Stores captured gateway request/response bodies for admin troubleshooting.
-- Capture is disabled by default and controlled by gateway_body_log_* settings.

CREATE TABLE IF NOT EXISTS gateway_body_logs (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(128) NOT NULL,
    api_key_id BIGINT NOT NULL,
    user_id BIGINT,
    account_id BIGINT,
    platform VARCHAR(32),
    model VARCHAR(128),
    request_type SMALLINT NOT NULL DEFAULT 0,
    stream BOOLEAN NOT NULL DEFAULT FALSE,
    request_method VARCHAR(16),
    request_path VARCHAR(256),
    inbound_endpoint VARCHAR(128),
    upstream_endpoint VARCHAR(128),
    client_request_id VARCHAR(128),
    request_content_type VARCHAR(128),
    response_content_type VARCHAR(128),
    status_code INTEGER NOT NULL DEFAULT 0,
    request_headers JSONB,
    response_headers JSONB,
    request_body BYTEA,
    response_body BYTEA,
    request_body_bytes BIGINT NOT NULL DEFAULT 0,
    response_body_bytes BIGINT NOT NULL DEFAULT 0,
    request_body_sha256 VARCHAR(64),
    response_body_sha256 VARCHAR(64),
    request_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    response_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    storage_kind VARCHAR(16) NOT NULL DEFAULT 'db',
    request_stored_at TIMESTAMPTZ,
    response_stored_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS gateway_body_logs_request_key_uq
    ON gateway_body_logs (request_id, api_key_id);

CREATE INDEX IF NOT EXISTS idx_gateway_body_logs_created_at
    ON gateway_body_logs (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_gateway_body_logs_api_key_created_at
    ON gateway_body_logs (api_key_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_gateway_body_logs_user_created_at
    ON gateway_body_logs (user_id, created_at DESC);
