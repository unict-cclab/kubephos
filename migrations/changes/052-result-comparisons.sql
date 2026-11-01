-- Comparisons reference source runs; they never own or copy experiment history.
CREATE TABLE result_comparisons (
    id text PRIMARY KEY,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    series jsonb NOT NULL CHECK (jsonb_typeof(series) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX result_comparisons_owner ON result_comparisons(owner_user_id, updated_at DESC);
