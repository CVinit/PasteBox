CREATE TABLE IF NOT EXISTS system_configs (
    id text PRIMARY KEY,
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS redemption_batches (
    id text PRIMARY KEY,
    plan_id text NOT NULL REFERENCES plans(id),
    duration_days integer NOT NULL,
    quantity integer NOT NULL,
    expires_at timestamptz,
    max_total_redemptions integer NOT NULL,
    max_redemptions_per_user integer NOT NULL,
    allowed_emails jsonb NOT NULL DEFAULT '[]'::jsonb,
    allowed_domains jsonb NOT NULL DEFAULT '[]'::jsonb,
    note text NOT NULL DEFAULT '',
    disabled boolean NOT NULL DEFAULT false,
    redeemed_count integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS redemption_batches_plan_id_idx ON redemption_batches(plan_id);
CREATE INDEX IF NOT EXISTS redemption_batches_disabled_idx ON redemption_batches(disabled);

CREATE TABLE IF NOT EXISTS redemption_codes (
    code_hash text PRIMARY KEY,
    batch_id text NOT NULL REFERENCES redemption_batches(id) ON DELETE CASCADE,
    redeemed_by text REFERENCES users(id) ON DELETE SET NULL,
    redeemed_at timestamptz,
    created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS redemption_codes_batch_id_idx ON redemption_codes(batch_id);
CREATE INDEX IF NOT EXISTS redemption_codes_redeemed_by_idx ON redemption_codes(redeemed_by);

CREATE TABLE IF NOT EXISTS redemption_records (
    id text PRIMARY KEY,
    code_hash text NOT NULL REFERENCES redemption_codes(code_hash),
    batch_id text NOT NULL REFERENCES redemption_batches(id),
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id text NOT NULL REFERENCES plans(id),
    created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS redemption_records_batch_user_idx ON redemption_records(batch_id, user_id);
CREATE INDEX IF NOT EXISTS redemption_records_user_id_idx ON redemption_records(user_id);

CREATE TABLE IF NOT EXISTS alert_events (
    id text PRIMARY KEY,
    fingerprint text NOT NULL,
    level text NOT NULL,
    message text NOT NULL,
    status text NOT NULL,
    last_error text NOT NULL DEFAULT '',
    sent_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS alert_events_fingerprint_updated_at_idx ON alert_events(fingerprint, updated_at DESC);
CREATE INDEX IF NOT EXISTS alert_events_status_idx ON alert_events(status);
