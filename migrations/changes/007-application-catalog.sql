CREATE TABLE catalog_applications (
    id text NOT NULL,
    version text NOT NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    origin text NOT NULL,
    descriptor jsonb NOT NULL,
    digest text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, version),
    CONSTRAINT catalog_applications_origin_check CHECK (origin IN ('built-in', 'imported'))
);

CREATE INDEX catalog_applications_name_idx ON catalog_applications(name, version);
