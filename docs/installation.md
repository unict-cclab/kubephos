# Installation and operations

## Install

Use a Linux host with Bash, OpenSSL, Docker Engine and Docker Compose 2.23.1 or
newer. Follow Docker's [Linux installation guide](https://docs.docker.com/engine/install/)
if Docker is not installed. Your account must be able to run `docker info`.

Keep `install.sh` and `compose.yaml` together. Run:

```bash
./install.sh
```

The installer asks for the initial administrator email and generates its password.
For non-interactive installation, pass `--admin-email you@example.org`.

The installation directory defaults to `~/.local/share/kubephos`. Override it with
`--directory /path/to/kubephos`. The installer generates:

| File | Purpose |
| --- | --- |
| `compose.yaml` | Installed services |
| `.env` | Image version, public URL, unique Compose project name and generated secrets |
| `credentials.json` | Initial KubePhos administrator username, email and password |
| `.installer-managed` | Marker allowing a later installer run to resume safely |

The two credential files have mode `0600`. Passwords are not printed or placed in
command arguments. Infrastructure secrets are independent from the application
administrator password. The credential encryption key is created in the private
`platform-data` volume when KubePhos starts.

`--prepare-only` creates the files without starting Docker. Rerunning the installer
keeps the existing files and secrets. It refuses to adopt an unrelated `.env` or
replace an existing installation's URL or version implicitly. A failed startup
keeps all files and volumes so it can be resumed.

The initial administrator is created through a one-shot container, before the app
and worker start. This is not a password reset mechanism. If you change the
administrator's password in the UI, `credentials.json` is only the original login
record; rerunning the installer does not reset that password.

## Network access

The default installation listens on `127.0.0.1:8080`.

```bash
./install.sh --url http://SERVER_IP:8080
```

An explicit remote HTTP URL publishes the port on the host interfaces (IPv4, or IPv6 for an IPv6 literal URL). For HTTPS,
use `--url https://kubephos.example.org` and configure a TLS reverse proxy to
`127.0.0.1:8080`. Forward the original `Host` and `X-Forwarded-Proto` headers.
KubePhos proxies its authentication service under `/auth`; do not expose Keycloak,
PostgreSQL or SeaweedFS separately.

After installation, edit `KUBEPHOS_URL`, `KUBEPHOS_PORT` and
`KUBEPHOS_BIND_ADDRESS` in the installed `.env` only as part of a planned URL
change. Keycloak imports the realm only on first startup: changing `.env` alone
does not update existing client redirect URLs or rotate existing Keycloak secrets.

## Manage services

Run commands from the installation directory:

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

## Update

Wait for experiments and cleanup operations to finish before updating the worker.
Back up the installation first. Use the Compose file supplied with the target
release, preserving `.env`, credentials, project name and volumes.

1. Stop the app and worker: `docker compose stop app worker`.
2. Set `KUBEPHOS_VERSION` in `.env` to the target published tag.
3. Run `docker compose pull`.
4. Run `docker compose up -d --wait --wait-timeout 300`.
5. Run `docker compose exec app kubephos doctor` and inspect service logs.

Liquibase applies new database changesets before the app or worker starts. A
release image includes the API, frontend, CLI, built-in plugins, application
catalog and migrations. Keep app, worker and migration-files on the same release.
Rolling back an image does not reverse a database migration; check compatibility
and restore a consistent backup if required.

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
