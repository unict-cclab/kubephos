ALTER TABLE provider_connections
    ADD COLUMN connectivity jsonb,
    ADD COLUMN connectivity_failures integer NOT NULL DEFAULT 0,
    ADD COLUMN connectivity_next_check_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN connectivity_lease_id text;
ALTER TABLE managed_resources
    ADD COLUMN connectivity jsonb,
    ADD COLUMN connectivity_failures integer NOT NULL DEFAULT 0,
    ADD COLUMN connectivity_next_check_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN connectivity_lease_id text;
