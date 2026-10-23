CREATE TABLE artifact_deletions (
    storage_key text PRIMARY KEY,
    queued_at timestamptz NOT NULL DEFAULT now(),
    retry_after timestamptz NOT NULL DEFAULT now()
);
