# Project review — 30 October 2026

## Changes

- Installation and development instructions now distinguish a published installer
  archive from an unreleased source checkout. Experiment and frontend quality
  guides describe current behavior without the previous chronological notes.
- The updater identifies an installation by its installer marker, checks pending
  work before changing files, and validates/downloads the release before replacing
  the installed Compose file. Invalid HTTPS ports are rejected by the installer.
- Shared lists adapt to their available width while keeping actions visible.
  Wide editable tables expose their field labels before controls become too narrow.
  Wizard navigation adapts to its container and no longer marks skipped steps as
  completed. Field alignment, CPU units and authorship fallback are consistent.
- API clients preserve valid lists on incomplete responses and provide actionable
  errors for connectivity, malformed responses and expired sessions. JSON editors
  validate their expected object/array shape before submission.
- Authentication cookies and Keycloak forwarding honor the configured HTTPS
  origin behind TLS-terminating proxies, without trusting arbitrary proxy headers.
- Request validation rejects oversized bodies, extra JSON values and non-object
  requests. Application errors use field labels with deterministic ordering;
  model-import errors omit internal network details and signed URLs.
- Suite plots filter unrelated metrics before processing samples. Script-test
  discovery is shared by Make and CI; PostgreSQL CI also runs coordinator tests.
- Added bounded, read-only connectivity monitoring with per-member multi cluster
  results, consecutive-failure confirmation, recovery and stale-result handling.
  Checks do not change provisioning or attribution; exported/imported state resets
  transient monitoring results and leases.
- Removed the unused standalone LocalAI example and the tracked placeholder for
  generated component-image archives. The build creates its output directory.

## Verification

- Root Go tests against disposable PostgreSQL; race checks for connectivity probes
  and exclusive monitor leases; tests and vet for all three managed
  Go modules; Cluster Lens theme regression.
- 336 frontend tests, including accessibility and wizard regressions; production
  build; both-theme contrast checks; initial assets below the 220 KiB gzip budget.
- 20 installer, updater and packaging tests; Bash syntax and ShellCheck.
- `npm audit`: no vulnerabilities. `govulncheck`: no reachable vulnerabilities in
  the root module or the three managed modules. These are dependency checks, not
  an image/operating-system security audit.
- Full Docker build and local app/worker health checks.
- Real installation with fresh volumes using published v0.1.3, followed by the
  revised updater and development image. Initial credentials, encryption key and
  a saved catalog edit survived; changing the public URL updated Keycloak's
  redirect URI and browser origin. All doctor checks passed.
- Authenticated browser walkthrough: Overview, all Infrastructure and Catalog
  lists, single/multi cluster forms, cluster details/access, new/edit configuration
  workflows, suite review/colors, sharing, experiment comparison/statistics/plots/
  exports, Users and Administration. Checked desktop, intermediate and phone
  widths, light/dark themes, empty search results and values retained across steps.

## Limits

The visual walkthrough did not submit provisioning, experiments, account or
sharing changes, credential downloads, or deletions. Automated tests cover those
interfaces where applicable. No new Proxmox provisioning benchmark, full external
backup/restore rehearsal, screen-reader session or representative-user usability
study was performed. Existing Proxmox VMs and cluster deployments were not changed by these checks.
