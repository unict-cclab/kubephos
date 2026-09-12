INSERT INTO workspaces (id, name, description, status)
SELECT 'ws_default', 'Default', 'Managed KubePhos environment', 'ready'
WHERE NOT EXISTS (SELECT 1 FROM workspaces);
