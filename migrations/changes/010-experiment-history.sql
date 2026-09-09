CREATE TABLE experiments (
    id text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    status text NOT NULL,
    result_type text NOT NULL,
    result_version text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (status IN ('succeeded'))
);

CREATE INDEX experiments_workspace_created_idx ON experiments(workspace_id, created_at DESC);
CREATE INDEX experiments_result_contract_idx ON experiments(result_type, result_version);

CREATE TABLE experiment_variants (
    id text PRIMARY KEY,
    experiment_id text NOT NULL REFERENCES experiments(id) ON DELETE CASCADE,
    position integer NOT NULL,
    name text NOT NULL,
    configuration jsonb NOT NULL DEFAULT '{}',
    UNIQUE(experiment_id, position),
    UNIQUE(experiment_id, name)
);

CREATE TABLE experiment_trials (
    id text PRIMARY KEY,
    variant_id text NOT NULL REFERENCES experiment_variants(id) ON DELETE CASCADE,
    position integer NOT NULL,
    status text NOT NULL,
    operation_id text NOT NULL REFERENCES operations(id) ON DELETE RESTRICT,
    result_artifact_id text NOT NULL REFERENCES artifacts(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz NOT NULL,
    UNIQUE(variant_id, position),
    CHECK (status IN ('succeeded'))
);

CREATE INDEX experiment_trials_operation_idx ON experiment_trials(operation_id);
