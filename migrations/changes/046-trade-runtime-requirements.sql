-- These requirements belong to the pinned KubePhos adapter, not to its QoS
-- configuration. Preserve parameters, authorship and active run snapshots.
UPDATE strategy_catalog
SET default_configuration = jsonb_set(
  default_configuration || '{"requiredReplicas":1,"startupEvent":"started"}'::jsonb,
  '{parameters}',
  to_jsonb(((default_configuration->>'parameters')::jsonb || '{"entrypoint":"auto"}'::jsonb)::text)
)
WHERE kind = 'orchestrator'
  AND source_image LIKE '%/trade@sha256:6628c07f3877e018948a538a41260551144b7544d827070b23208be6eddd84fd';
