ALTER TABLE experiment_variants
    ADD COLUMN display_alias text,
    ADD COLUMN plot_color text;

UPDATE experiment_variants
SET display_alias = name
WHERE display_alias IS NULL;

ALTER TABLE experiment_variants
    ALTER COLUMN display_alias SET NOT NULL,
    ADD CONSTRAINT experiment_variants_display_alias_check CHECK (char_length(display_alias) BETWEEN 1 AND 80),
    ADD CONSTRAINT experiment_variants_plot_color_check CHECK (plot_color IS NULL OR plot_color ~ '^#[0-9A-Fa-f]{6}$');
