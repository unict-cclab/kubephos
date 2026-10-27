# Architecture and configuration

## Services

| Service | Responsibility |
| --- | --- |
| App | API, web UI, authentication proxy, CLI and validation |
| Worker | Background infrastructure operations and experiments |
| PostgreSQL | Durable configuration, state, history and logs |
| SeaweedFS | Generated artifacts, datasets and figures |
| Keycloak | User credentials and browser authentication |
| Liquibase | Ordered database migrations before startup |
| Auth sync | One-shot update of managed Keycloak client settings before startup |

App, worker and the migration-files helper use the same KubePhos image. Persistent
volumes belong to the installation's Compose project. The installer generates a
unique project name; development explicitly uses `kubephos-dev`.

Plugins provide infrastructure, application, placement, load and monitoring
capabilities through versioned contracts in `contracts/` and `plugins/`.
Applications are declared in `catalog/applications/`. Managed component versions
are defined in `internal/managedcomponents/components.go`.

## Installation settings

The installer asks only for the installation path, browser URL and image version
when their defaults are unsuitable. It generates the database password, OIDC
client secrets and Keycloak bootstrap password. Internal service URLs, directories,
realm names and client IDs are fixed by the installed Compose configuration.

The `.env.example` file documents the generated settings; it is not a usable
configuration until the secret fields are populated. The updater synchronizes OIDC client secrets. Database and Keycloak
administrator passwords must also be changed in their persisted accounts; editing
`.env` alone does not rotate those passwords.

The public URL must match the browser URL because it determines authentication
callbacks. After editing it, run the installed `update.sh` to synchronize Keycloak
and restart the services. The application listens on all host interfaces;
`KUBEPHOS_PORT` controls the published port.

## Advanced execution settings

Core CLI settings remain available for non-Compose integrations in
`internal/config/config.go`. Worker concurrency and poll interval use built-in
defaults; change them only for a measured operational need, with a Compose override
setting `KUBEPHOS_WORKER_CONCURRENCY` and `KUBEPHOS_WORKER_POLL_INTERVAL`.

Remote OCI plugin execution is optional. To enable it, add
`deploy/compose.plugins.yaml` after the base Compose file and configure the remote
runtime host, CA, client certificate and private key. Mount certificate files
read-only through your own override when paths are used. The same settings must
reach both app and worker. `KUBEPHOS_PLUGIN_ALLOWED_REGISTRIES` restricts image
sources; `KUBEPHOS_BUILD_ALLOWED_GIT_HOSTS` defaults to `github.com`.

These advanced settings are intentionally absent from the basic installer.
Managed clusters, Harbor, NFS and cloud images are configured in the UI rather
than global environment variables.

Managed NFS storage uses `kubephos/<installation marker>/<PV name>` for each
volume directory. This isolates clusters sharing the same export and PVCs
recreated under the same namespace and name. Existing PVs retain their original
paths; changing the provisioning rule does not migrate their data.

Cluster Retry keeps the saved settings and resource identity. It validates and
removes resources left by the previous attempt before provisioning again with
the current plugins. Retry is blocked during active experiments or lifecycle
operations; previous attempts remain available in the history.
If only the final ClusterMesh stage fails, Retry reuses the completed operations
and verified outputs, and runs only the mesh stage. No VM or storage replacement
is performed in that case.

## Portable state archives

Administrator-only state transfer uses a password-encrypted `.kps` archive.
`internal/storage/transfer.go` explicitly lists portable tables and follows
infrastructure operation/artifact dependencies; experiment execution data and
identity tables are excluded. Database migration checksums must match before an
import. Extend this table list and the transfer integration tests when adding
new persistent configuration; incompatible archive shapes need a new format
version.

The archive contains decrypted infrastructure credentials only inside its
authenticated encryption envelope. Import re-encrypts credentials and sensitive
artifacts with the destination vault, preserves resource IDs, remaps configuration
ownership, and keeps installation secrets and Keycloak state untouched. Metadata
is committed in one transaction after object uploads; cleanup directories are
registered in the artifact deletion outbox before uploading and removed from the
outbox in the same transaction on success. Failed or interrupted imports leave
retryable object cleanup, rather than partially imported resources.

Managed node bootstrap persists minimum inotify limits (8192 instances and
1048576 watches per user) before installing Kubernetes, preserving larger
existing limits. This leaves room for Cilium and the managed monitoring agents
when their workloads restart or roll out.
