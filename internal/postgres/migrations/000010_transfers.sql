CREATE TABLE IF NOT EXISTS transfers (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    paste_id text NOT NULL REFERENCES pastes(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'draft',
    idempotency_key text NOT NULL DEFAULT '',
    share_id text NOT NULL DEFAULT '',
    password_hash text NOT NULL DEFAULT '',
    login_required boolean NOT NULL DEFAULT false,
    expires_at timestamptz NOT NULL,
    published_at timestamptz,
    canceled_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS transfers_user_id_idx ON transfers(user_id);
CREATE INDEX IF NOT EXISTS transfers_paste_id_idx ON transfers(paste_id);

CREATE UNIQUE INDEX IF NOT EXISTS transfers_user_idempotency_key_idx
ON transfers(user_id, idempotency_key)
WHERE idempotency_key <> '';

CREATE TABLE IF NOT EXISTS transfer_items (
    transfer_id text NOT NULL REFERENCES transfers(id) ON DELETE CASCADE,
    item_id text NOT NULL,
    file_name text NOT NULL,
    content_type text NOT NULL DEFAULT '',
    size_bytes bigint NOT NULL DEFAULT 0,
    attachment_id text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'pending',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (transfer_id, item_id)
);

CREATE INDEX IF NOT EXISTS transfer_items_attachment_id_idx ON transfer_items(attachment_id);
