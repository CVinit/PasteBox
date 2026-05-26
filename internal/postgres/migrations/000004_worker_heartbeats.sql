CREATE TABLE IF NOT EXISTS worker_heartbeats (
    worker_id text PRIMARY KEY,
    last_seen_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS worker_heartbeats_last_seen_at_idx ON worker_heartbeats(last_seen_at DESC);
