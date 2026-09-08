CREATE TABLE credentials (
    id text PRIMARY KEY,
    name text NOT NULL,
    kind text NOT NULL,
    fingerprint text NOT NULL,
    nonce bytea NOT NULL,
    ciphertext bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(kind, name)
);

CREATE INDEX credentials_kind_name_idx ON credentials(kind, name);
