ALTER TABLE artifacts
    ADD COLUMN storage_digest text,
    ADD COLUMN stored_size_bytes bigint,
    ADD COLUMN encryption_nonce bytea;

UPDATE artifacts
SET storage_digest = digest,
    stored_size_bytes = size_bytes
WHERE storage_digest IS NULL OR stored_size_bytes IS NULL;

ALTER TABLE artifacts
    ALTER COLUMN storage_digest SET NOT NULL,
    ALTER COLUMN stored_size_bytes SET NOT NULL;

ALTER TABLE artifacts
    ADD CONSTRAINT artifacts_encryption_state_check
    CHECK (
        (sensitive = false AND encryption_nonce IS NULL)
        OR
        (sensitive = true AND encryption_nonce IS NOT NULL)
    );
