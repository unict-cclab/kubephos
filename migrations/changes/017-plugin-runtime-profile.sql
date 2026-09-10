CREATE TABLE plugin_runtime_profiles (
    id text PRIMARY KEY CHECK (id = 'default'),
    endpoint_artifact_id text NOT NULL REFERENCES artifacts(id),
    credential_artifact_id text NOT NULL REFERENCES artifacts(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plugin_runtime_registries (
    profile_id text NOT NULL REFERENCES plugin_runtime_profiles(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0 AND position < 16),
    endpoint_artifact_id text NOT NULL REFERENCES artifacts(id),
    credential_artifact_id text NOT NULL REFERENCES artifacts(id),
    PRIMARY KEY (profile_id, position),
    UNIQUE (profile_id, endpoint_artifact_id),
    UNIQUE (profile_id, credential_artifact_id)
);
