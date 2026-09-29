-- Pickup codes let a recipient type 6 characters instead of pasting a long
-- share link. A code is only ever stored with the share it belongs to, so
-- revocation and expiry keep working through the existing share rules.
--
-- Shares created before this migration keep an empty code and stay link-only.
ALTER TABLE shares
    ADD COLUMN IF NOT EXISTS pickup_code text NOT NULL DEFAULT '';

-- A code is unique among every stored code, including revoked and expired
-- ones, so a code that was once handed out can never point at another share.
CREATE UNIQUE INDEX IF NOT EXISTS shares_pickup_code_idx
ON shares(pickup_code)
WHERE pickup_code <> '';

-- Guess counting is shared across API instances, so adding processes cannot
-- multiply a guesser's budget. The window restarts in place per key.
CREATE TABLE IF NOT EXISTS pickup_code_attempts (
    attempt_key text PRIMARY KEY,
    window_start timestamptz NOT NULL,
    attempt_count integer NOT NULL
);

CREATE INDEX IF NOT EXISTS pickup_code_attempts_window_start_idx
ON pickup_code_attempts(window_start);
