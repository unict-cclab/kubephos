CREATE TABLE catalog_application_previews (
    application_id text NOT NULL,
    application_version text NOT NULL,
    application_digest text NOT NULL,
    content jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id, application_version),
    FOREIGN KEY (application_id, application_version) REFERENCES catalog_applications(id, version) ON DELETE CASCADE
);
