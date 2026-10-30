# Development

## Local stack

Use Docker Engine and Compose 2.23.1 or newer:

```bash
make up
make logs
make doctor
make down
```

`make up` builds the platform and managed component images, then starts the
`kubephos-dev` project on `127.0.0.1:8088`. `compose.dev.yaml` overrides the
installation Compose file and `deploy/dev.env` supplies public development-only
credentials. These commands explicitly select the development files and do not
read a checkout's private `.env`. Development volumes are separate from installed
instances. The development URL uses `127.0.0.1` to avoid sharing authentication
cookies with the default `localhost` installation. For browser smoke tests against
other instances, use a different hostname or a separate browser profile: ports
alone do not isolate cookies. Do not switch an existing installation to this project name.

For direct Compose commands:

```bash
docker compose --env-file deploy/dev.env -f compose.yaml -f compose.dev.yaml ps
```

No Docker socket is mounted into the app or worker. Docker-based plugin execution
requires the optional remote runtime described in [architecture](architecture.md).

## Frontend development

With the development stack running, use Node 24.20, matching CI:

```bash
npm --prefix frontend ci
npm --prefix frontend run dev
```

Open **http://127.0.0.1:5173** using the same hostname as the stack. Sign in at
http://127.0.0.1:8088 first; the session cookie also works on the Vite port.
Authentication callbacks return to the stack port. Vite proxies API and
authentication requests to that stack.

## Checks

Host requirements: Go as pinned in `go.mod`, Node as used by CI, Python 3, Bash,
OpenSSL and jq. Run:

```bash
make test
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
npm --prefix frontend run build
npm --prefix frontend run check:bundle
npm --prefix frontend run check:theme
python3 -m unittest discover -s scripts -p 'test_*.py'
```

Database integration tests require a **disposable** PostgreSQL instance. Set
`KUBEPHOS_TEST_DATABASE_URL` to that instance and run:

```bash
go test ./internal/storage ./internal/api ./internal/engine -v
```

The tests create isolated schemas. Never point them at an operational database.
The CI PostgreSQL job runs storage, API and pipeline-coordinator tests against its
own service.
Frontend tests include accessibility checks and wizard navigation regressions.
Each managed Go component has a separate module and is included in `make test`.

## Releases

Push the commit to a branch before creating its version tag. Checks validates all
four Go modules, PostgreSQL, the frontend and the installer. The release workflow
waits for that same commit's latest branch-push Checks run instead of repeating
its tests. A failed, cancelled or missing check run blocks publication; rerunning
Checks and then the release gate does not require another commit or tag.

After checks and release validation succeed, the release workflow publishes the
platform image and an installer archive containing the matching Compose file. Prerelease tags must not move `latest`.

Managed component versions live in `internal/managedcomponents/components.go`.
Increment a component version whenever its source changes; existing published
versions must not be repurposed. Imported strategies retain their own versions
and image digests. The release workflow refuses to replace a published managed
component version with a different source tree. Older images without a source-tree
label require a new version before publication.

Before publishing, test both a fresh installation and an upgrade from the previous
release, including a backup/restore check. Review the compatibility of migrations,
plugin contracts and persisted experiment datasets. Never edit applied migrations.
