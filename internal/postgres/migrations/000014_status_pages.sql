CREATE INDEX IF NOT EXISTS transfers_user_created_page_idx
ON transfers(user_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS pastes_user_updated_page_idx
ON pastes(user_id, updated_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS transfer_claims_live_idx
ON transfer_claims(transfer_id, expires_at)
WHERE status = 'active';
