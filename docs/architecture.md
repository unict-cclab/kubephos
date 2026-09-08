# Architecture

## Runtime boundary

The core persists workspaces, validated plans, state transitions, leases, logs and artifacts. It does not contain provider, Kubernetes distribution, application or strategy logic.

Capabilities are supplied by versioned plugins. A plugin validates configuration without side effects, produces an immutable plan, executes its steps and proves their post-conditions.

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

## Managed infrastructure access

Harbor, NFS and SSH access are mediated by backend plugins. Credentials stay server-side. Imported resources are read-only by default, writes require capabilities and destructive actions require explicit confirmation.
