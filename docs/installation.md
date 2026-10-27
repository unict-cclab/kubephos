# Installation and operations

## Install

Use a Linux host with Bash, OpenSSL, Docker Engine and Docker Compose 2.23.1 or
newer. Follow Docker's [Linux installation guide](https://docs.docker.com/engine/install/)
if Docker is not installed. Your account must be able to run `docker info`.

Keep `install.sh`, `update.sh` and `compose.yaml` together. Both scripts can be
invoked by absolute path from any working directory. Run:

```bash
./install.sh
```

The installer asks for the initial administrator email and generates a temporary
password. At first login, you must set a new password before accessing KubePhos.
For non-interactive installation, pass `--admin-email you@example.org`.

The installation directory defaults to `~/.local/share/kubephos`. Override it with
`--directory /path/to/kubephos`. The installer generates:

| File | Purpose |
| --- | --- |
| `compose.yaml` | Installed services |
| `update.sh` | Apply configuration and update from any working directory |
| `install.log`, `update.log` | Private detailed operation logs |
| `.env` | Image version, public URL, unique Compose project name and generated secrets |
| `credentials.json` | Initial KubePhos administrator username, email and temporary password |
| `.installer-managed` | Marker allowing a later installer run to resume safely |

Read the initial login credentials from `~/.local/share/kubephos/credentials.json`:

```bash
cat ~/.local/share/kubephos/credentials.json
```

If you installed using `--directory /path/to/installation`, the file is
`/path/to/installation/credentials.json`. The installer prints its exact path
at the end.

The two credential files have mode `0600`. Passwords are not printed or placed in
command arguments. Infrastructure secrets are independent from the application
administrator password. The credential encryption key is created in the private
`platform-data` volume when KubePhos starts.

`--prepare-only` creates the files without starting Docker. Rerunning the installer
keeps the existing files and secrets. It refuses to adopt an unrelated `.env` or
replace an existing installation's URL or version implicitly. A failed startup
keeps all files and volumes so it can be resumed.

The initial administrator is created through a one-shot container, before the app
and worker start. After the mandatory first-login password change,
`credentials.json` still contains the original temporary password and is not
updated with your new password. Rerunning the installer or updater does not reset
an existing account's password or force another password change. Existing
installations retain their current login credentials.

## Proxmox provisioning

VM identifiers are checked across the Proxmox cluster, including guests on offline
nodes. An unavailable peer does not block provisioning on an available node:
network checks use reachable guests and the reservations already saved in KubePhos.
The selected destination node must still be online. Authentication, permission
and malformed-response errors remain explicit.

## Network access

The application listens on all host interfaces on port 8080 by default.
`KUBEPHOS_PORT` changes the published port. Set the browser origin when installing:

```bash
/path/to/release/install.sh --url http://SERVER_IP:8080
```

For HTTPS, use `--url https://kubephos.example.org` and configure a TLS reverse
proxy to port 8080. Forward the original `Host` and `X-Forwarded-Proto` headers.
KubePhos proxies authentication under `/auth`; PostgreSQL, Keycloak and SeaweedFS
have no published host ports.

To change the instance address, edit `KUBEPHOS_URL` in the installed `.env` and
run `./update.sh` from your clone or extracted release directory. Change
`KUBEPHOS_PORT` as well when changing the
listener port. The update synchronizes Keycloak's callback URI, web origin and
logout redirect with the configured URL, preserving users and client settings.

## Update

From your clone or extracted release directory, run:

```bash
./update.sh
```

By default, this uses the existing installation in `~/.local/share/kubephos`.
Edit `~/.local/share/kubephos/.env` to set the desired `KUBEPHOS_VERSION`,
`KUBEPHOS_URL` or `KUBEPHOS_PORT` before running the command. You do not need to
change into the installation directory.

For an installation created with a custom path:

```bash
./update.sh --directory /path/to/installation
```

Use the script from the release or checkout matching your target image version.
It copies its matching Compose and update script into the installation while
preserving `.env`, credentials, project name and volumes. The installer also
places a copy of `update.sh` in the installation directory, which can be invoked
by absolute path when you only need to reapply configuration.
It downloads images, checks for running operations, stops app and worker,
applies migrations, synchronizes authentication and restarts the services.
`--no-pull` applies configuration using images already present on the host.

Wait for experiments and cleanup operations to finish and back up the installation
before updating. Generated database and Keycloak administrator passwords describe
existing accounts: editing those passwords in `.env` alone does not rotate them.
OIDC client secrets are synchronized by the authentication configuration step.
For a legacy installation whose Keycloak administrator is named `admin`, set
`KUBEPHOS_KEYCLOAK_ADMIN_USERNAME=admin` in `.env` before its first update.

Both scripts display high-level progress. Compose output is written to private
`install.log` or `update.log` in the installation directory. On failure, the script
reports the failed phase and log path; data and credentials remain available.

Liquibase applies new database changesets before app or worker starts. Keep app,
worker and migration-files on the same release. Rolling back an image does not
reverse a database migration; check compatibility and restore a consistent backup
if required.

## Manage services

For direct Docker Compose commands, use the installation directory:

```bash
cd ~/.local/share/kubephos
docker compose ps -a
docker compose logs --tail=100 app worker
docker compose exec app kubephos doctor
docker compose stop
docker compose up -d --wait
```

Docker restarts the long-running services after host reboot. Do not run
`docker compose down -v` unless you intend to delete the installation's data.

## Move instance state to another server

Administrators can use **Administration → Export / Import** to move
infrastructure and reusable configuration to a new KubePhos instance.

1. Wait for queued and running experiments, infrastructure operations and plugin
   imports to finish on the source instance.
2. Open **Administration → Export**, choose an archive password of at least 12 characters,
   confirm it and select **Export state**. Keep the downloaded `.kps` file and its
   password: both are required for import.
3. Install KubePhos on the new server using the same release. Keep the new
   installation's generated `.env`, credentials and encryption key.
4. Sign in as an administrator on the destination. Before adding resources, open
   **Administration → Import**, choose the `.kps` file, enter its archive password and select
   **Review archive**. Check the source version, date and resource counts, then
   select **Import state**.
5. Stop the old instance before managing the transferred infrastructure from the
   new one. Ensure the new server can reach the existing Proxmox servers, VMs,
   Kubernetes APIs, registries and remote plugin runtime, if configured.

The archive contains Proxmox connections and credentials, VM templates, Harbor,
NFS and cluster records and dependencies, application/model/strategy catalogs,
experiment configurations and suites, installed plugin descriptors, remote runtime
settings and infrastructure artifacts such as kubeconfigs and certificates.
Infrastructure provisioning metadata and resource authorship labels are retained;
operation logs and unrelated execution history are excluded.

Users, login passwords, sessions, sharing permissions, cluster assignments,
experiments, runs and collected experiment results are excluded. Imported
configurations become private and belong to the administrator performing the
import. If different source owners used the same configuration name, additional
names receive an `(imported N)` suffix; suite references remain intact.

The export never contains `.env` or the source instance's credential encryption
key. Connection credentials and sensitive artifacts are re-encrypted with the
destination key. The archive itself is encrypted with its separate password.
Neither archive passwords nor decrypted credentials are returned in the review.
The destination's public URL, Keycloak clients and service credentials remain
unchanged.

Import requires a fresh destination with no infrastructure, custom catalogs,
credentials, configurations or experiments. A matching database migration schema
is required. Metadata is committed together after artifact uploads succeed;
failed imports roll back metadata and queue cleanup of temporary objects.
Archives are limited to 128 MiB.

This moves KubePhos management state. It does not copy VMs, container images,
model caches or data stored on NFS/Harbor, and does not reprovision infrastructure.
Use the backup procedure below when you need the full experiment history or a
complete disaster-recovery backup.

## Backups

A complete recoverable backup includes the installed `.env`, `credentials.json`,
Compose file and all persistent data:

- PostgreSQL: users, configuration, state, processing logs and history.
- SeaweedFS: datasets, figures, generated manifests and other artifacts.
- `platform-data`: the encryption key for stored infrastructure credentials.

Stop the app and worker and wait for ongoing writes to finish before taking a
consistent database/object-store backup. Use `pg_dump` for PostgreSQL or stop the
whole stack before snapshotting its volumes. Restore the matching database,
objects and encryption key together. Verify recovery on a separate installation.

## Troubleshooting

If installation fails, inspect `docker compose ps -a` and logs for `migrate`,
`keycloak`, `app` and `worker`. Rerun the installer after resolving the cause;
it retains generated credentials. If interrupted by a power failure, check that no
installer is running before removing a stale `.install-lock` directory.

The installer never upgrades Docker, changes a Proxmox cluster or deletes volumes.
