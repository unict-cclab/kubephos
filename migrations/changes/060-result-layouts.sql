-- Each user's arrangement of the charts on an experiment's or a comparison's results
-- page: which charts, in which order, each a KubeStudio image. Removed with the user
-- and with the results it arranges.
CREATE TABLE result_layouts (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    experiment_id text REFERENCES experiments(id) ON DELETE CASCADE,
    comparison_id text REFERENCES result_comparisons(id) ON DELETE CASCADE,
    layout jsonb NOT NULL CHECK (jsonb_typeof(layout) = 'object'),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((experiment_id IS NULL) <> (comparison_id IS NULL))
);
CREATE UNIQUE INDEX result_layouts_experiment ON result_layouts(user_id, experiment_id) WHERE experiment_id IS NOT NULL;
CREATE UNIQUE INDEX result_layouts_comparison ON result_layouts(user_id, comparison_id) WHERE comparison_id IS NOT NULL;
