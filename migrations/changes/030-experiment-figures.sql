CREATE TABLE experiment_figures (
    id text PRIMARY KEY,
    experiment_id text NOT NULL REFERENCES experiments(id) ON DELETE CASCADE,
    metric text NOT NULL,
    format text NOT NULL CHECK (format IN ('png', 'pdf')),
    source_artifact_ids text[] NOT NULL,
    storage_key text NOT NULL UNIQUE,
    digest text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 8388608),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX experiment_figures_experiment_created_idx ON experiment_figures(experiment_id, created_at DESC);
