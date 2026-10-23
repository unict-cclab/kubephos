# Installation and repository review — October 2026

## Changes

- Added a Linux installer with generated, independent secrets and an initial
  administrator provisioned before the application port is published.
- Kept installation configuration separate from development. The development
  override has its own project, volumes, port and explicit local credentials.
- Removed fallback development secrets from the installation Compose file and
  core Keycloak configuration. Internal service addresses and advanced runtime
  parameters no longer appear in the basic installation path.
- Fixed the health-check CLI to use the internal HTTP listener, so a different
  public port or an HTTPS reverse proxy does not make a healthy container fail.
- Removed the unused status CLI, which queried an authenticated endpoint without
  credentials; doctor is the supported health-check command.
- Removed the duplicate, unused managed-stack version manifest and editor backups;
  moved the standalone LocalAI workload example into examples/.
- Excluded local state, agent configuration, credentials and Python caches from
  publication/build contexts. Kept public development credentials explicitly named.
- Added the MIT license, contribution/security guides and third-party notices.
  Preserved Mentat's notice in its image and incremented that component version.
- Simplified the README and separated operational, development and experiment
  documentation. Installation archives pin their image version and include checksums.
- Prevented prereleases from updating latest and protected published managed
  component versions using source-tree labels.
- Expanded CI from a subset to the full storage test package; included installer,
  and packaging checks. Fixed two stale frontend
  navigation assertions and gave the full multi cluster wizard test a suitable
  timeout rather than changing the application behavior.
- Updated Vitest, the Go compiler and the vulnerable PostgreSQL, cryptographic,
  network and text dependencies. Added reachable-vulnerability scans to CI.
  Normalized dependency manifests for all Go modules.

## Verification

The review ran the root Go tests and vet using Go 1.26.10, tests/vet for all three managed Go
modules, the complete frontend suite and production build, frontend bundle/theme
checks, the complete storage package on disposable PostgreSQL, installer and
packaging regressions, Compose validation, ShellCheck, npm audit and govulncheck.
The frontend passed its test suite; npm audit reported zero vulnerabilities. The core
and all three managed modules reported zero reachable Go vulnerabilities after
the updates. Transitive modules can still contain advisory-listed packages that
are not reachable from these programs; this is not an image/OS vulnerability scan.

A separate Compose installation with fresh volumes was used to verify migrations,
Keycloak realm import, generated administrator credentials, a custom public port,
all health checks, repeated administrator setup without resetting accounts, and a
real browser login to the empty dashboard. It did not reuse the operational
installation's database, object storage, encryption key or volumes. Browser tests
must also isolate hostnames/profiles, as described in the development guide.

## Publication boundaries

The default installer is ready to be packaged with its matching platform image.
No GitHub release or registry image was published during this review. Registry
publication and the matching release download still require a release run.

These checks do not replace an upgrade/restore rehearsal from the previous
published release, a deployment on every Linux distribution or new Proxmox/VM
provisioning experiments. Those operations were deliberately not run on existing
clusters during repository cleanup.
