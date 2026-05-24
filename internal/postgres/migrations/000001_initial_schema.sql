CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint PRIMARY KEY,
    name text NOT NULL,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS plans (
    id text PRIMARY KEY,
    name text NOT NULL,
    active_paste_limit integer NOT NULL,
    active_storage_bytes bigint NOT NULL,
    single_text_bytes bigint NOT NULL,
    single_file_bytes bigint NOT NULL,
    single_paste_bytes bigint NOT NULL,
    attachments_per_paste_limit integer NOT NULL,
    max_retention_seconds bigint NOT NULL,
    daily_upload_bytes bigint NOT NULL,
    daily_share_download_bytes bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS prices (
    id text PRIMARY KEY,
    plan_id text NOT NULL REFERENCES plans(id),
    period text NOT NULL,
    amount_cents bigint NOT NULL,
    currency text NOT NULL,
    visible boolean NOT NULL DEFAULT true,
    purchase_enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (plan_id, period)
);

CREATE TABLE IF NOT EXISTS users (
    id text PRIMARY KEY,
    email text NOT NULL UNIQUE,
    display_name text NOT NULL,
    language text NOT NULL DEFAULT 'en',
    password_hash text NOT NULL,
    role text NOT NULL DEFAULT 'user',
    email_verified boolean NOT NULL DEFAULT false,
    plan_id text NOT NULL REFERENCES plans(id),
    plan_expires_at timestamptz,
    frozen boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    delete_requested_at timestamptz,
    delete_scheduled_at timestamptz,
    deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS users_plan_id_idx ON users(plan_id);
CREATE INDEX IF NOT EXISTS users_deleted_at_idx ON users(deleted_at);

CREATE TABLE IF NOT EXISTS sessions (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);

CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions(user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS auth_tokens (
    hash text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email text NOT NULL,
    kind text NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS auth_tokens_user_kind_idx ON auth_tokens(user_id, kind);
CREATE INDEX IF NOT EXISTS auth_tokens_expires_at_idx ON auth_tokens(expires_at);

CREATE TABLE IF NOT EXISTS login_failures (
    email text PRIMARY KEY,
    count integer NOT NULL,
    window_start timestamptz NOT NULL,
    locked_until timestamptz
);

CREATE TABLE IF NOT EXISTS pastes (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title text NOT NULL,
    text_body text NOT NULL DEFAULT '',
    tags jsonb NOT NULL DEFAULT '[]'::jsonb,
    pinned boolean NOT NULL DEFAULT false,
    favorite boolean NOT NULL DEFAULT false,
    status text NOT NULL DEFAULT 'active',
    scan_status text NOT NULL DEFAULT 'pending',
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS pastes_user_id_idx ON pastes(user_id);
CREATE INDEX IF NOT EXISTS pastes_status_expires_at_idx ON pastes(status, expires_at);
CREATE INDEX IF NOT EXISTS pastes_tags_gin_idx ON pastes USING gin(tags);

CREATE TABLE IF NOT EXISTS attachments (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    paste_id text NOT NULL REFERENCES pastes(id) ON DELETE CASCADE,
    file_name text NOT NULL,
    content_type text NOT NULL,
    size_bytes bigint NOT NULL,
    sha256 text NOT NULL,
    object_key text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    scan_status text NOT NULL DEFAULT 'pending',
    risk text NOT NULL DEFAULT '',
    image_width integer NOT NULL DEFAULT 0,
    image_height integer NOT NULL DEFAULT 0,
    download_count bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS attachments_user_id_idx ON attachments(user_id);
CREATE INDEX IF NOT EXISTS attachments_paste_id_idx ON attachments(paste_id);
CREATE INDEX IF NOT EXISTS attachments_sha256_idx ON attachments(sha256);
CREATE INDEX IF NOT EXISTS attachments_scan_status_idx ON attachments(scan_status);

CREATE TABLE IF NOT EXISTS object_refs (
    object_key text PRIMARY KEY,
    ref_count integer NOT NULL DEFAULT 0,
    size_bytes bigint NOT NULL DEFAULT 0,
    sha256 text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS shares (
    id text PRIMARY KEY,
    paste_id text NOT NULL REFERENCES pastes(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    token_ciphertext text NOT NULL DEFAULT '',
    password_hash text NOT NULL DEFAULT '',
    login_required boolean NOT NULL DEFAULT false,
    max_visits integer NOT NULL DEFAULT 0,
    max_downloads integer NOT NULL DEFAULT 0,
    visit_count integer NOT NULL DEFAULT 0,
    download_count integer NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    last_visited_at timestamptz,
    last_downloaded_at timestamptz,
    last_access_failure timestamptz
);

CREATE INDEX IF NOT EXISTS shares_paste_id_idx ON shares(paste_id);
CREATE INDEX IF NOT EXISTS shares_user_id_idx ON shares(user_id);
CREATE INDEX IF NOT EXISTS shares_expires_at_idx ON shares(expires_at);

CREATE TABLE IF NOT EXISTS daily_metrics (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    metric_kind text NOT NULL,
    metric_day date NOT NULL,
    bytes bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, metric_kind, metric_day)
);

CREATE TABLE IF NOT EXISTS orders (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider text NOT NULL,
    plan_id text NOT NULL REFERENCES plans(id),
    period text NOT NULL,
    amount_cents bigint NOT NULL,
    currency text NOT NULL,
    status text NOT NULL,
    checkout_url text NOT NULL DEFAULT '',
    address text NOT NULL DEFAULT '',
    chain text NOT NULL DEFAULT '',
    tx_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    expires_at timestamptz,
    paid_at timestamptz
);

CREATE INDEX IF NOT EXISTS orders_user_id_idx ON orders(user_id);
CREATE INDEX IF NOT EXISTS orders_provider_status_idx ON orders(provider, status);

CREATE TABLE IF NOT EXISTS webhook_events (
    id text PRIMARY KEY,
    provider text NOT NULL,
    event_type text NOT NULL,
    target_id text NOT NULL DEFAULT '',
    idempotency_key text NOT NULL UNIQUE,
    processed boolean NOT NULL DEFAULT false,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    received_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS webhook_events_provider_idx ON webhook_events(provider);
CREATE INDEX IF NOT EXISTS webhook_events_target_id_idx ON webhook_events(target_id);

CREATE TABLE IF NOT EXISTS audit_logs (
    id text PRIMARY KEY,
    actor_id text NOT NULL DEFAULT '',
    action text NOT NULL,
    target text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS audit_logs_actor_id_idx ON audit_logs(actor_id);
CREATE INDEX IF NOT EXISTS audit_logs_created_at_idx ON audit_logs(created_at);

CREATE TABLE IF NOT EXISTS reports (
    id text PRIMARY KEY,
    user_id text REFERENCES users(id) ON DELETE SET NULL,
    target text NOT NULL,
    reason text NOT NULL,
    status text NOT NULL DEFAULT 'open',
    created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS reports_status_idx ON reports(status);

CREATE TABLE IF NOT EXISTS jobs (
    id text PRIMARY KEY,
    kind text NOT NULL,
    target_id text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    last_error text NOT NULL DEFAULT '',
    run_after timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS jobs_status_run_after_idx ON jobs(status, run_after);
CREATE INDEX IF NOT EXISTS jobs_kind_target_idx ON jobs(kind, target_id);

CREATE TABLE IF NOT EXISTS mails (
    id text PRIMARY KEY,
    recipient text NOT NULL,
    subject text NOT NULL,
    body text NOT NULL,
    status text NOT NULL DEFAULT 'queued',
    attempts integer NOT NULL DEFAULT 0,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    sent_at timestamptz
);

CREATE INDEX IF NOT EXISTS mails_status_created_at_idx ON mails(status, created_at);

INSERT INTO plans (
    id,
    name,
    active_paste_limit,
    active_storage_bytes,
    single_text_bytes,
    single_file_bytes,
    single_paste_bytes,
    attachments_per_paste_limit,
    max_retention_seconds,
    daily_upload_bytes,
    daily_share_download_bytes
) VALUES
    ('free', 'Free', 20, 524288000, 262144, 26214400, 52428800, 5, 86400, 1073741824, 2147483648),
    ('plus', 'Plus', 500, 53687091200, 2097152, 262144000, 1073741824, 20, 2592000, 21474836480, 107374182400),
    ('pro', 'Pro', 5000, 536870912000, 10485760, 2147483648, 5368709120, 100, 15552000, 214748364800, 1099511627776)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    active_paste_limit = EXCLUDED.active_paste_limit,
    active_storage_bytes = EXCLUDED.active_storage_bytes,
    single_text_bytes = EXCLUDED.single_text_bytes,
    single_file_bytes = EXCLUDED.single_file_bytes,
    single_paste_bytes = EXCLUDED.single_paste_bytes,
    attachments_per_paste_limit = EXCLUDED.attachments_per_paste_limit,
    max_retention_seconds = EXCLUDED.max_retention_seconds,
    daily_upload_bytes = EXCLUDED.daily_upload_bytes,
    daily_share_download_bytes = EXCLUDED.daily_share_download_bytes,
    updated_at = now();

INSERT INTO prices (
    id,
    plan_id,
    period,
    amount_cents,
    currency,
    visible,
    purchase_enabled
) VALUES
    ('price_plus_monthly', 'plus', 'monthly', 900, 'USD', true, true),
    ('price_plus_yearly', 'plus', 'yearly', 9000, 'USD', true, true),
    ('price_pro_monthly', 'pro', 'monthly', 2900, 'USD', true, true),
    ('price_pro_yearly', 'pro', 'yearly', 29000, 'USD', true, true)
ON CONFLICT (id) DO UPDATE SET
    plan_id = EXCLUDED.plan_id,
    period = EXCLUDED.period,
    amount_cents = EXCLUDED.amount_cents,
    currency = EXCLUDED.currency,
    visible = EXCLUDED.visible,
    purchase_enabled = EXCLUDED.purchase_enabled,
    updated_at = now();
