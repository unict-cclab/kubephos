# Interface review — 5 November 2026

## Changes

- The console uses a layered monochrome theme: a tinted frame, a raised page sheet
  and cards, with color reserved for status. Stylesheets load through explicit
  cascade layers (`tokens`, `legacy`, `console`, `components`, `shell`) from
  `frontend/src/design/index.css`; the shared legacy stylesheet is unchanged.
- Navigation has four sections. Platform and Experiments are single pages whose
  related views appear as ordered steps (provision → create → deploy, configure →
  run → compare). Existing view hashes still work. A command palette (Ctrl/⌘ K)
  reaches every page; an account menu holds password and account links to the
  Keycloak account console, theme, refresh, backup, sign-out and the version;
  administrators carry an "Admin" badge next to their name.
- "New experiment" is the primary action on every page. `#results/new` opens the
  launcher; without a runnable configuration the action explains that one is needed.
- Overview shows the setup steps that remain and a recent-activity timeline. The
  application catalog is a searchable card grid. Shared resource lists keep their
  columns, sorting and display options with relative dates, author avatars and
  row actions revealed on hover or focus.
- The Keycloak sign-in theme follows the same tokens; its stylesheet is now
  `kubephos-v3.css` so browsers do not reuse the previous version.

## Verification

- 434 frontend tests, including accessibility, navigation, command palette,
  account menu and workflow-step tests; type check; production build; both-theme
  contrast checks; initial assets at 184.9 KiB gzip, below the 220 KiB budget.
- `npm audit`: no vulnerabilities.
- Development Keycloak recreated with the new theme; the sign-in page links
  `kubephos-v3.css` and the account console pages used by the account menu respond.
- Authenticated browser walkthrough at desktop and phone widths in light and dark
  themes: Home, Platform (empty infrastructure and cluster lists), Library (cards
  and import form), Experiments (empty runs and configurations), Members, account
  menu, command palette and mobile navigation.

## Limits

The development instance had no clusters or experiments, so result statistics,
plots, cluster and deployment details and creation wizards with real data were not
reviewed visually in the new design; they use the shared components covered above.
The sign-in page was verified by its served stylesheet rather than a visual
session. No backend code changed, so Go tests were not rerun. No screen-reader
session or representative-user study was performed.

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
