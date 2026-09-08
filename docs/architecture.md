# Architecture

## Runtime boundary

The core persists workspaces, validated plans, state transitions, leases, logs and artifacts. It does not contain provider, Kubernetes distribution, application or strategy logic.

Capabilities are supplied by versioned plugins. A plugin validates configuration without side effects, produces an immutable plan, executes its steps and proves their post-conditions.

Plugin manifests also declare credential schemas and permissions. The frontend renders those schemas without provider-specific fields in the core.

## Operation lifecycle

```text
configuration
    -> validation
    -> immutable plan
    -> user confirmation
    -> queue
    -> precheck
    -> execute
    -> health gate
    -> next step
```

An operation cannot be queued with a different plan hash from the validated plan. A step succeeds only after its health report is healthy.

## Processes

- `kubephos serve` runs the API and embedded frontend.
- `kubephos worker` runs a concurrent worker pool.
- PostgreSQL coordinates durable queue claims with row locks and leases.
- Liquibase installs and upgrades the schema before application processes start.
- SeaweedFS stores binary and high-volume artifacts.

## Credential boundary

Credential values are validated against the schema declared by the plugin, encrypted with AES-GCM and stored in PostgreSQL. The encryption key is generated locally and shared by API and worker through a private persistent volume.

Operations, plans, logs and artifacts contain only opaque credential references. At invocation time the runtime resolves those references and supplies values only when the plugin manifest declares `secrets.read:<kind>`.

## Managed infrastructure access

Harbor, NFS and SSH access are mediated by backend plugins. Credentials stay server-side. Imported resources are read-only by default, writes require capabilities and destructive actions require explicit confirmation.

The first Proxmox plugin exposes only discovery and preflight. Its HTTP client supports GET requests and marks every existing VM or template as imported and read-only.
