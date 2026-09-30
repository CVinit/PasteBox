-- Anonymous claim slots and claim sessions for a published transfer. Claiming
-- spends one slot; a file claim opens a bounded session covering the whole
-- batch, while a text claim only stays replayable for a short recovery window.
--
-- Transfers created before this migration keep the default of one slot, which
-- matches the send default. Legacy shares that never went through a transfer
-- keep their own access rules and are not claim-gated.
ALTER TABLE transfers
    ADD COLUMN IF NOT EXISTS claim_quota integer NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS claimed_count integer NOT NULL DEFAULT 0;

-- Looking a share up by its transfer is what decides whether the claim gate
-- applies, so the lookup must be indexed.
CREATE INDEX IF NOT EXISTS transfers_share_id_idx
ON transfers(share_id)
WHERE share_id <> '';

CREATE TABLE IF NOT EXISTS transfer_claims (
    id text PRIMARY KEY,
    transfer_id text NOT NULL REFERENCES transfers(id) ON DELETE CASCADE,
    share_id text NOT NULL DEFAULT '',
    kind text NOT NULL DEFAULT 'file',
    operation_id text NOT NULL DEFAULT '',
    token text NOT NULL DEFAULT '',
    token_hash text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'active',
    expires_at timestamptz NOT NULL,
    completed_at timestamptz,
    created_at timestamptz NOT NULL
);

-- One operation id claims once per transfer, so a retried claim request returns
-- the claim that already spent the slot instead of spending a second one.
CREATE UNIQUE INDEX IF NOT EXISTS transfer_claims_operation_idx
ON transfer_claims(transfer_id, operation_id)
WHERE operation_id <> '';

CREATE UNIQUE INDEX IF NOT EXISTS transfer_claims_token_hash_idx
ON transfer_claims(token_hash)
WHERE token_hash <> '';

CREATE INDEX IF NOT EXISTS transfer_claims_share_id_idx
ON transfer_claims(share_id);
