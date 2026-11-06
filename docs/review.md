# Frontend review — 6 November 2026

- Integrated the visual foundation from `feature/frontend-refresh` at `92423b6`,
  retaining tabular catalog lists, explicit dates and visible row actions.
- Cluster editing and read-only configuration share section navigation and pool
  components. Properties, labels and taints belong to the selected pool; system
  pool properties remain protected. Application pools use named tabs.
- Infrastructure, experiment configuration, deployment and suite details follow
  their corresponding editor sections. Small read-only tables remain compact in
  adjacent columns; field labels and values have consistent typography.
- Load preview stays beside the phase editor. Common controls align actions with
  tabs and retain values while changing sections.
- Catalog application details separate General, Topology, Services, deployment
  manifests and Locust scripts into tabs. Files stay lazy-loaded and cached while
  switching sections. Placement overrides use named service tabs like resources.
- Resource activity separates progress and process logs without interrupting log
  polling. Log filters survive navigation and live follow resumes on opening.
  Shared tabs have consistent spacing; nested sections avoid duplicate
  dividers. Unused catalog-card styles from the reference branch were removed.
- Errors use temporary notifications with retry controls retained. Creating a
  comparison requires two valid single configurations. Dark surfaces, controls,
  borders and topology edges have stronger contrast; decorative motion was removed.
- Results statistics separate Overall and By service while retaining filters;
  print includes both sections. Statistic headers and values align consistently.
  Suite plots, comparison statistics and individual runs were also checked with
  synthetic data in an isolated local preview.
- Grouped the existing sidebar destinations under Infrastructure, Experiments
  and Administration, retaining direct links for Overview, Deployments and
  Catalog. The account footer separates username and role and shows a
  readable version row using the version reported by the running backend.
- Validation: frontend tests passed, including suite results, navigation,
  placement drafts, notifications, activity polling and hidden-tab log behavior.
  Production build, asset budget, both-theme contrast checks and npm audit passed.
  Authenticated server checks covered catalog details, creation navigation,
  placement overrides, network phases, infrastructure, user lists and activity.
  Results and comparison plots were inspected with synthetic data in an isolated
  local preview; no experiments or infrastructure were created for verification.
- Inline field validation preserves focus and alignment. Comparison actions
  remain disabled until their prerequisites are met and explain why on hover
  or keyboard focus. Defaults remain read-only beside editable placement settings.
- Final verification: 473 frontend tests, full Go and component tests, isolated
  PostgreSQL storage/API/engine tests, production build, theme contrast, asset
  budget and frontend dependency audit passed.

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
