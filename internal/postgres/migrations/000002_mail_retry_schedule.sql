ALTER TABLE mails
ADD COLUMN IF NOT EXISTS run_after timestamptz;

UPDATE mails
SET run_after = created_at
WHERE run_after IS NULL;

ALTER TABLE mails
ALTER COLUMN run_after SET NOT NULL,
ALTER COLUMN run_after SET DEFAULT now();

CREATE INDEX IF NOT EXISTS mails_status_run_after_idx ON mails(status, run_after);
