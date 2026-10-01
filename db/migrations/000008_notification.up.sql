CREATE TABLE IF NOT EXISTS notification_outbox (
    id SERIAL PRIMARY KEY,
    kind VARCHAR(64) NOT NULL,
    incident_id INTEGER NOT NULL REFERENCES incident(id),
    recipient VARCHAR(255) NOT NULL,
    payload JSONB NOT NULL,
    change_id UUID NOT NULL,
    dedup_key VARCHAR(255) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NULL,
    locked_by VARCHAR(255) NULL,
    locked_at TIMESTAMPTZ NULL,
    last_error TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_outbox_dispatch
    ON notification_outbox (next_attempt_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_outbox_stale_processing
    ON notification_outbox (locked_at)
    WHERE status = 'processing';

CREATE UNIQUE INDEX IF NOT EXISTS idx_outbox_dedup
    ON notification_outbox (dedup_key);

-- Supports retention pruning and the sent-count ops stat.
CREATE INDEX IF NOT EXISTS idx_outbox_retention
    ON notification_outbox (updated_at)
    WHERE status = 'sent';
