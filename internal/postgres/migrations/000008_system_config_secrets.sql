CREATE TABLE IF NOT EXISTS system_config_secrets (
    config_id text NOT NULL REFERENCES system_configs(id) ON DELETE CASCADE,
    name text NOT NULL,
    version integer NOT NULL,
    nonce bytea NOT NULL,
    ciphertext bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (config_id, name)
);
