CREATE TABLE catalog_models (
 id text PRIMARY KEY,
 definition jsonb NOT NULL,
 origin text NOT NULL CHECK (origin IN ('built-in', 'custom')),
 enabled boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
