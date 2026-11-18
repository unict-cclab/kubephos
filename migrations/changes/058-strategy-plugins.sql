-- Each catalog strategy names the plugin that runs it, because one kind can have several
-- implementations: the image-based Custom Pod Autoscaler and the built-in Kubernetes HPA,
-- whose entries carry only scaling defaults and no source image.
ALTER TABLE strategy_catalog ADD COLUMN plugin_id text;
UPDATE strategy_catalog SET plugin_id = CASE kind
    WHEN 'scheduler' THEN 'io.kubephos.scheduler.kubernetes.secondary'
    WHEN 'descheduler' THEN 'io.kubephos.descheduler.kubernetes.managed'
    ELSE 'io.kubephos.autoscaler.kubernetes.cpa'
END;
ALTER TABLE strategy_catalog ALTER COLUMN plugin_id SET NOT NULL;
