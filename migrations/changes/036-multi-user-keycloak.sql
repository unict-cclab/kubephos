ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users
    ALTER COLUMN password_hash SET DEFAULT '',
    ADD COLUMN external_id text,
    ADD COLUMN email text NOT NULL DEFAULT '',
    ADD COLUMN enabled boolean NOT NULL DEFAULT true;

UPDATE users SET role = 'user' WHERE role IN ('operator', 'viewer');

ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin', 'user'));

CREATE UNIQUE INDEX users_external_id_idx ON users(external_id) WHERE external_id IS NOT NULL;

ALTER TABLE experiment_configurations ADD COLUMN owner_user_id text REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE experiments ADD COLUMN owner_user_id text REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE pipeline_runs
    ADD COLUMN owner_user_id text REFERENCES users(id) ON DELETE RESTRICT,
    ADD COLUMN isolation_mode text NOT NULL DEFAULT 'exclusive',
    ADD CONSTRAINT pipeline_runs_isolation_mode_check CHECK (isolation_mode IN ('namespaced', 'exclusive'));

UPDATE experiment_configurations SET owner_user_id = (SELECT id FROM users ORDER BY created_at LIMIT 1) WHERE owner_user_id IS NULL;
UPDATE experiments SET owner_user_id = (SELECT id FROM users ORDER BY created_at LIMIT 1) WHERE owner_user_id IS NULL;
UPDATE pipeline_runs run SET owner_user_id = experiment.owner_user_id
FROM experiment_trials trial
JOIN experiment_variants variant ON variant.id = trial.variant_id
JOIN experiments experiment ON experiment.id = variant.experiment_id
WHERE trial.pipeline_run_id = run.id AND run.owner_user_id IS NULL;
UPDATE pipeline_runs SET owner_user_id = (SELECT id FROM users ORDER BY created_at LIMIT 1) WHERE owner_user_id IS NULL;

ALTER TABLE experiment_configurations ALTER COLUMN owner_user_id SET NOT NULL;
ALTER TABLE experiments ALTER COLUMN owner_user_id SET NOT NULL;

CREATE INDEX experiment_configurations_owner_idx ON experiment_configurations(owner_user_id, updated_at DESC);
CREATE INDEX experiments_owner_idx ON experiments(owner_user_id, created_at DESC);
CREATE INDEX pipeline_runs_owner_active_idx ON pipeline_runs(owner_user_id, status) WHERE owner_user_id IS NOT NULL;
DROP INDEX experiment_configurations_workspace_name_idx;
CREATE UNIQUE INDEX experiment_configurations_owner_name_idx ON experiment_configurations(owner_user_id, lower(name));

CREATE TABLE cluster_user_assignments (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cluster_resource_id text NOT NULL REFERENCES managed_resources(id) ON DELETE CASCADE,
    assigned_by text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, cluster_resource_id)
);

CREATE INDEX cluster_user_assignments_cluster_idx ON cluster_user_assignments(cluster_resource_id);

CREATE FUNCTION guard_pipeline_run_concurrency() RETURNS trigger AS $$
BEGIN
    IF NEW.status <> 'running' OR (TG_OP = 'UPDATE' AND OLD.status = 'running') THEN
        RETURN NEW;
    END IF;
    IF NEW.owner_user_id IS NOT NULL THEN
        PERFORM pg_advisory_xact_lock(hashtextextended('user:' || NEW.owner_user_id, 0));
        IF EXISTS (
            SELECT 1 FROM pipeline_runs active
            WHERE active.owner_user_id = NEW.owner_user_id AND active.id <> NEW.id AND active.status = 'running'
        ) THEN
            RAISE EXCEPTION 'user already has an active experiment' USING ERRCODE = '23505';
        END IF;
    END IF;
    IF NEW.cluster_resource_id IS NOT NULL THEN
        PERFORM pg_advisory_xact_lock(hashtextextended('cluster:' || NEW.cluster_resource_id, 0));
        IF EXISTS (
            SELECT 1 FROM pipeline_runs active
            WHERE active.cluster_resource_id = NEW.cluster_resource_id AND active.id <> NEW.id AND active.status = 'running'
              AND (NEW.isolation_mode = 'exclusive' OR active.isolation_mode = 'exclusive')
        ) THEN
            RAISE EXCEPTION 'cluster has a conflicting active experiment' USING ERRCODE = '23505';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER pipeline_runs_concurrency_guard
BEFORE INSERT OR UPDATE OF status ON pipeline_runs
FOR EACH ROW
EXECUTE FUNCTION guard_pipeline_run_concurrency();
