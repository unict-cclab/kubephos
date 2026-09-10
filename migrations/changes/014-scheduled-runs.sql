ALTER TABLE pipeline_runs ADD COLUMN scheduled_for timestamptz NOT NULL DEFAULT now();
UPDATE pipeline_runs SET scheduled_for = queued_at;
CREATE INDEX pipeline_runs_claim_idx ON pipeline_runs(status, scheduled_for, created_at);

ALTER TABLE experiments ADD COLUMN scheduled_for timestamptz;
