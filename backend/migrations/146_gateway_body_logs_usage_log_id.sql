-- Re-key gateway body logs as children of usage_logs.

ALTER TABLE gateway_body_logs
    ADD COLUMN IF NOT EXISTS usage_log_id BIGINT;

UPDATE gateway_body_logs g
SET usage_log_id = u.id
FROM usage_logs u
WHERE g.usage_log_id IS NULL
  AND u.request_id = g.request_id
  AND u.api_key_id = g.api_key_id;

DELETE FROM gateway_body_logs
WHERE usage_log_id IS NULL;

ALTER TABLE gateway_body_logs
    ALTER COLUMN usage_log_id SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'gateway_body_logs_usage_log_id_fkey'
          AND conrelid = 'gateway_body_logs'::regclass
    ) THEN
        ALTER TABLE gateway_body_logs
            ADD CONSTRAINT gateway_body_logs_usage_log_id_fkey
            FOREIGN KEY (usage_log_id) REFERENCES usage_logs(id) ON DELETE CASCADE;
    END IF;
END $$;

DROP INDEX IF EXISTS gateway_body_logs_request_key_uq;

CREATE UNIQUE INDEX IF NOT EXISTS gateway_body_logs_usage_log_id_uq
    ON gateway_body_logs (usage_log_id);
