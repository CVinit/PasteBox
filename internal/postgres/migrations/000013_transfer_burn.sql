-- Burn-after-reading is a per-send switch. It is off by default and is only
-- ever set at send time, so transfers and shares created before this migration
-- keep their current lifetime and nothing starts destroying old content.
ALTER TABLE transfers
    ADD COLUMN IF NOT EXISTS burn_after_reading boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS destroyed_at timestamptz,
    ADD COLUMN IF NOT EXISTS destroy_reason text NOT NULL DEFAULT '';

-- The burn sweep looks for published burn transfers that are due, so the scan
-- stays bounded by the transfers that opted in rather than the whole table.
CREATE INDEX IF NOT EXISTS transfers_burn_due_idx
ON transfers(status, expires_at)
WHERE burn_after_reading;
