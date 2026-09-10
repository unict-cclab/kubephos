ALTER TABLE operations
    ADD COLUMN plugin_version text NOT NULL DEFAULT '',
    ADD COLUMN plugin_digest text NOT NULL DEFAULT '';
