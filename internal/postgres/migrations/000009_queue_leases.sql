ALTER TABLE jobs
ADD COLUMN IF NOT EXISTS claimed_by text NOT NULL DEFAULT '',
ADD COLUMN IF NOT EXISTS lease_expires_at timestamptz;

ALTER TABLE mails
ADD COLUMN IF NOT EXISTS claimed_by text NOT NULL DEFAULT '',
ADD COLUMN IF NOT EXISTS lease_expires_at timestamptz;

CREATE INDEX IF NOT EXISTS jobs_runnable_lease_idx
ON jobs(status, run_after, lease_expires_at);

CREATE INDEX IF NOT EXISTS mails_runnable_lease_idx
ON mails(status, run_after, lease_expires_at);
