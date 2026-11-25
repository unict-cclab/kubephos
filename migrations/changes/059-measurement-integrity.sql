-- Idempotent on purpose: development databases may already have applied this
-- change under its earlier id 058-measurement-integrity, before 058 was taken by
-- 058-strategy-plugins.

-- A cleanup failure after a verified result keeps the result and records the
-- failure as a warning. Later sequential runs are still canceled.
ALTER TABLE pipeline_runs ADD COLUMN IF NOT EXISTS cleanup_error text NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs DROP CONSTRAINT IF EXISTS pipeline_runs_terminal_status_check;
ALTER TABLE pipeline_runs ADD CONSTRAINT pipeline_runs_terminal_status_check
    CHECK (terminal_status IS NULL OR terminal_status IN ('succeeded', 'failed', 'canceled'));
ALTER TABLE experiment_trials
    ADD COLUMN IF NOT EXISTS cleanup_error text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS pilot boolean NOT NULL DEFAULT false;

-- Optional rest between consecutive runs and a pilot run per configuration.
ALTER TABLE experiments
    ADD COLUMN IF NOT EXISTS rest_seconds_between_trials integer NOT NULL DEFAULT 0
        CHECK (rest_seconds_between_trials BETWEEN 0 AND 3600),
    ADD COLUMN IF NOT EXISTS first_trial_pilot boolean NOT NULL DEFAULT false;

-- Comparisons freeze their resolved sources. NULL keeps the legacy live
-- resolution until the comparison is saved or refreshed again.
ALTER TABLE result_comparisons
    ADD COLUMN IF NOT EXISTS snapshot jsonb CHECK (snapshot IS NULL OR jsonb_typeof(snapshot) = 'object');
-- Result artifacts frozen by a comparison survive deletion of their experiment.
CREATE TABLE IF NOT EXISTS result_comparison_artifacts (
    comparison_id text NOT NULL REFERENCES result_comparisons(id) ON DELETE CASCADE,
    artifact_id text NOT NULL,
    experiment_id text NOT NULL,
    PRIMARY KEY (comparison_id, artifact_id)
);
CREATE INDEX IF NOT EXISTS result_comparison_artifacts_artifact ON result_comparison_artifacts(artifact_id);
