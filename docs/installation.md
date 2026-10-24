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
