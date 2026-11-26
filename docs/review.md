# Results figures review — 25 November 2026

- The load and latency chart adds "Ready pods" (ready replicas summed over the
  application Deployments) under the server-side request rate, and a Download menu (PDF,
  PNG 600 dpi, SVG). The on-screen chart is flattened into one standalone SVG
  with explicit colours (`timeChartExport.ts`) and goes through the shared
  exporter.
- KubeStudio lives in `components/results/kubestudio/`: the editor
  (`KubeStudio.tsx`, 1326 → about 1040 lines) and its parts — `image.tsx`
  (building and drawing an image's charts, shared with the results page),
  `memory.ts`, `history.ts`, `selection.ts`, `icons.tsx`, `layout.ts`,
  `recipes.tsx`, `checks.ts`, `controls.tsx`, `ExportPopover.tsx` and the figure
  spec, model, code and editor files. Removed: the reader of the pre-multi-chart
  memory format, two unused helpers, and `export` from names used only in their
  own file.
- Charts: every results page (experiment or saved comparison) opens on a grid
  of charts. By default it holds the placement heatmap (when there is one), then
  each metric over time followed by the same metric by service; there is no
  separate "All metrics" section any more. Each user arranges their own grid:
  drag a chart onto the edge of another to move it there, or onto its middle to
  swap the two (Alt + arrow keys on its ⋯ button move it too); drag its
  bottom-right corner (or use the arrow keys on it) to make it span one to three
  columns, never more than the page has, so nothing scrolls sideways; right-click
  it or use its ⋯ button to edit it in KubeStudio, rename it (also by
  double-clicking its title), duplicate it, download it (PDF, PNG, SVG) or remove
  it from the page, with an undo. The placement heatmap is a bare figure that
  scales to its card (the card gives its title and downloads). The card menu was
  clipped by the card's `content-visibility: auto` and never showed; that
  property is gone. "Add chart" opens KubeStudio,
  and "Add to results" puts the image in the grid (or replaces the chart it was
  opened from).
- The simple chart cards (input load, topology, call chain) download PDF, SVG
  and PNG, each button with the download icon (its slot was empty).
- A completed execution shows one quiet line ("Execution completed in 5m ·
  input load 3m, 2 steps", with Process logs) instead of the status panel and the
  input load progress chart; the input load profile opens from that line. A
  failed, running or warned execution keeps the full panels.
- Display (which sections the page shows) is a page command in the header,
  left of Export, no longer on the row of the run tabs.
- The rest of the page (summary, load and latency, services, sources,
  endpoints, call chain, statistics, environment & methods, placement) starts
  hidden and is added from the grid's Sections menu; following a finding of the
  summary opens its section. Printing shows every section. "Reset" restores the
  default page (charts and sections), with an undo. The summary is a plain list
  of findings with a link to their section, and the services table has a fixed
  title.
- The arrangement (charts and shown sections) is kept on the server, per user and per experiment or
  comparison: table `result_layouts` (migration 060, removed with the user,
  the experiment or the comparison) and
  `GET/PUT/DELETE /api/v1/account/result-layouts/{experiments|comparisons}/{id}`.
  A user can only keep a layout for results they can read (owner or shared);
  otherwise the answer is 404. A layout is a JSON object of at most 512 KiB.
  A free selection of runs has no layout: changes last until the page closes.
  This replaces the per-browser "Figures made with KubeStudio".
- KubeStudio, simpler:
  - Charts of one image are moved by dragging their handle onto another chart
    (or with the arrow keys on it); duplicate and delete are on the chart's
    right-click menu. The Arrange group and its buttons are gone.
  - The caption is optional: right-click › Add caption, shown under the figure,
    click to edit, "Remove caption" to drop it. Without one, exports describe the
    figure with the charts' own summaries. The caption drawer and the "Methods &
    provenance" panel are gone (methods are still embedded in every export).
  - "Grid lines" is in the chart's settings ("Chart settings"), no longer under
    the colours; palettes show only their name, a series colour only its picker
    and hex value (with a warning below 3:1 contrast).
  - Line charts carry no decorative markers (on screen, PDF, matplotlib and
    pgfplots). Points stay where they are the data: scatter charts ("Point",
    "Point size", "Hollow"), dot plots and the percentile ladder.
  - Axes are not in Layers any more: a click on an axis title renames it in
    place (Enter or a click elsewhere keeps it, Esc cancels, an empty title
    restores the automatic one) and selects the axis, whose other settings show
    on the right; the chart's properties also link to its axes.
  - "Add chart" is guided: the chart-type picker opens first and the chosen chart
    is added and selected, with its X and Y on the right. A new figure from the
    results page ("Add chart", "Create figure") opens on the same picker.
  - The Legend properties list its entries, each a colour (click to change it)
    beside its text (the same text as the series' own label). A single plain
    line gets a legend once its entry has a text.
  - Number fields are typed freely, with a comma or a dot: the text is never
    rewritten while typing, a number applies at once, leaving the field shows
    the value in use. (A type="number" field turned "0," into an empty value,
    which reset the SLO.)
  - SLO line: shown and typed in ms or s; "Time above the SLO" gives, per
    series, the share of measured windows above and below it (windows, not
    single requests), optionally written in its label.
  - Lines drawn on the chart: right-click hides an automatic one (SLO, load
    phases, warm-up) or deletes the user's own; a click on a line's label
    renames it in place (an empty text restores the automatic one). The
    highlight of a selected line stays inside the plot.
  - "Uncertainty" shows only when a series has repetitions; the panels control
    of old saved charts is under "Chart settings".
  - Palettes are a list: each with its name, a short description and its
    colours in the order series take them.
  - "Add annotation" is now "Add a line in the graph", and each kind says what it
    is for, with an example (a threshold such as the 350 ms SLO, a shaded time
    range, a vertical line at an event, a text label).
- Removed as unused after these changes: `AllMetrics.tsx`, `XYRelationship.tsx`,
  `XYChart.tsx`, `ResultFigures.tsx` and `savedFigures.ts` (with their tests and
  CSS), `QualityNotices`, `isLatencyMetric`, `acrossRunsText`, `timelineHeadline`,
  the unused XY helpers, the line-marker sampling, and the styles of the old
  summary, the figure settings panel, the metric chips, the table notes and the
  heatmap grid.
- Validation: typecheck, all frontend tests (927; new ones for the charts grid,
  its persistence and the KubeStudio right-click menu, drag handle and caption),
  the asset budget (219.3 of 220 KiB), the theme contrast check, `go vet`, and
  the API and storage tests against PostgreSQL (new
  `TestResultLayoutsArePerUserAndFollowResultAccess`) passed. Not yet checked in
  the browser against real data.

# Load profile review — 24 November 2026

- Each rate of the workload phases shows the plain Locust users that would send
  that rate: rate × mean `wait_time` / requests per task,
  with the request mix currently in the form and without response time (a lower
  bound). The `wait_time` of the scenario's User classes is read from the Locust
  file (`between`, `constant`, `constant_pacing`, `constant_throughput` with
  literal arguments, also inherited from a local base class); without one no
  figure is shown. The generator is unchanged: it still paces the rate itself.
- Next to the virtual users, each rate also shows the estimated server-side
  rate: Locust counts a request whose redirect it follows once, the entry point
  receives both. Catalog endpoints declare their redirects
  (`spec.interface.endpoints[].redirects`, method and path, validated); the
  extra requests per task come from the Locust file's calls and the current
  mix. Online Boutique declares `POST /setCurrency`, `POST /cart`,
  `POST /cart/empty` and `GET /logout`: 28 server requests per 23 Locust
  requests, so 300 req/s reach the node proxies as about 365, as measured.
  Both figures appear in a full-width list under each phase's fields (one line
  per rate, wrapping when long), in the wizard's review step and in the
  configuration's workload phases (`rateEquivalents.ts`).
- The scenario's Locust file is fetched once per application and shared by the
  Request mix tab and the workload phases (`scenarioScripts.ts`); the parser is
  still loaded on demand.
- Online Boutique's node proxies run an nginx-prometheus-exporter sidecar that
  reads nginx `stub_status` (served on 127.0.0.1 only) and is scraped by the
  managed Prometheus through the usual pod annotations. Results gain
  `ingress.request_rps` (source nginx): requests that reach the entry point,
  including the redirects Locust follows within one of its requests; it is
  optional and absent for applications without nginx counters. The results'
  load chart shows it as "Request rate, server side" right under the Locust
  rate, now titled "Request rate, Locust side".
- In a configuration's Scheduling and scaling tabs, a kind without a component
  says what happens instead (default Kubernetes scheduler, no descheduling,
  initial replicas and resources kept).
- The browser tab icon is the sidebar brand mark (Geist "K" on the primary
  color, inverted in dark mode) instead of the older blue drawing.
- Validation: typecheck, all frontend tests (one navigation test failed once
  under full-suite load and passed three times alone), the asset budget, and
  Go vet and tests of the catalog, metrics collector, application inspector and
  deployer passed. Not yet run on a cluster.

# Istio settings review — 10 November 2026

- The Istio tab asks first whether the run uses Istio. "No" submits no sidecar,
  waypoint or proxy settings; "Yes" shows the data plane, then the sidecar
  settings or, for ambient mode, the waypoint size. Saved configurations open
  on their actual choice, and the read-only view shows it.
- Ambient configurations set the waypoint replicas (1 to 10) and per-replica
  CPU and memory requests and limits (`definition.istioWaypoint`), validated
  with the same quantity rules as proxy resources by the API and the deployer.
  The deployer overlays the Deployment Istio generates through a ConfigMap in
  `infrastructure.parametersRef` and waits for every replica; without these
  settings the waypoint manifest is unchanged.
- The waypoint editor has a Placement tab with the node selector and toleration
  fields of application placement (`nodeSelector`, `tolerations` in
  `definition.istioWaypoint`), validated with the same rules. A selector such
  as `kubernetes.io/hostname` pins the waypoint to a node; a tainted node, such
  as the control plane, also needs a matching toleration. The deployer adds them
  to the same Deployment overlay and waits for the replicas, so a selector that
  no node satisfies fails the deployment instead of leaving the waypoint pending.
- The deployer's `plugin.yaml` now accepts `istioWaypoint`; it was missing, so
  runs with a sized waypoint failed validation with "Property is not allowed".
  A test now requires `plugin.yaml` and the runtime manifest to declare the
  same configuration properties.
- "Enable Istio" is a checkbox. In ambient mode the waypoint editor has Size and
  Services tabs: every service uses the waypoint by default (or none, when
  unchecked) and per-service exceptions include or exclude single services.
  The deployer labels the matching Kubernetes Services with
  `istio.io/use-waypoint` (`waypoint` or `none`); the namespace carries the
  label only when every service uses the waypoint. Excluded services stay in
  the ambient mesh but lose L7 processing and request metrics.
- The Istio tab reads top to bottom: an "Enable Istio" switch that says what
  the choice means, a Mode choice between Sidecar and Ambient as two described
  cards, and one panel with that mode's settings (Sidecars, or Waypoint with
  Size and Service overrides tabs). Injection and waypoint use show their
  default, a hint on how to limit them to some services, and the overrides in
  one view instead of nested tabs.
- The Application step opens each section with one line on what it is for.
  Initial replicas by service form one compact list showing the inherited
  value, and empty placement says what happens without settings, with its add
  actions beneath the heading.
- Deploy application offers the same Istio choices, ambient included. The
  deployment pipeline installs ambient mode on the cluster before deploying
  and passes the waypoint size to the deployer; multi-cluster deployments,
  which run Cilium, still refuse ambient mode with an explanation.
- Validation: Go vet and tests (`internal/workloadresources`, `internal/api`,
  `internal/storage`, application deployer) with a disposable PostgreSQL,
  frontend tests, typecheck, build, asset budget and contrast passed. The editor
  was checked in an isolated review page; no run was started on a cluster.

# Requests and ownership review — 10 November 2026

- Members edit the text of their own open requests in place and can mark them
  resolved when they solve them; the card then reads "Resolved by you".
  Replying, reopening and other members' requests stay out of reach (the API
  answers 403, or 404 for someone else's request). Administrators can delete
  any request after confirming it (`DELETE /api/v1/admin-requests/{id}`).
- Ask an admin uses two columns: the member's requests, grouped as Open and
  Resolved, beside a sticky New request card with a meter of the three open
  requests allowed; phones show the form first. Resolved requests fold into a
  collapsed section showing the five newest; administrators see the ten newest
  resolved requests. Older ones appear on demand.
- Administrators search the inbox by username: part of a name, in any case,
  narrows both tabs and their counts, with suggestions of users who sent
  requests and a Clear search action when nothing matches.
- Configurations and Runs show the Created by filter only when the list mixes
  creators (members also see items shared with them) or a choice is active.
- Validation: Go vet and the `internal/api` and `internal/storage` tests with a
  disposable PostgreSQL, frontend tests, typecheck, build, asset budget and
  contrast checks passed. The member page was checked at desktop and phone
  widths in both themes with sample data in an isolated preview; the running
  development backend still predates these endpoints.

# Frontend review — 9 November 2026

- Resource lists with many columns no longer clip their row actions at laptop
  widths: wide lists use a tighter column gutter and scroll inside their card
  when they still exceed it.
- The sidebar keeps only the group of the current page open after navigation,
  so it no longer outgrows the viewport; its scrollbar follows the theme.
- On phones, setup steps keep their Done/Next state on the step row; only the
  current action wraps below the description.
- Open requests drop the colored edge on their rounded card and use a secondary
  Resolve action, keeping one solid ink button per view.
- Removed stylesheet rules that later layers fully overrode (inverted account
  dock, mono version and dates, unused sidebar groups and menu identity) and
  merged duplicated declarations, so each component is styled in one place.
- `refinements.css` is gone: its rules now live with the components and shell
  rules they corrected. Field states use `:where()` selectors with rising
  specificity (base, hover, invalid, focus) instead of a later override layer.
- Import states before an archive is chosen what it would replace, using the
  new `GET /api/v1/administration/state/readiness`. On an instance with data,
  importing asks to overwrite everything; on confirmation the import sends
  `replace=true` and, in the same transaction, clears the transferred state,
  experiments and saved comparisons before restoring the archive. Users,
  sessions and the audit log are kept; old stored objects are queued for
  deletion by exact key, so newly imported objects are never removed. Running
  work still blocks the import. Import never merges.
- Empty lists show only their empty state (and filters); dates use the
  interface language with a 24-hour clock; the sorted column reads in full ink;
  disabled fields and file pickers look disabled.
- Usability: clicking a list row opens its details (row controls excepted);
  configuration types describe themselves and show why Comparison is
  unavailable; phone list toolbars keep search, count and display on one row.
- Validation: frontend tests, typecheck, production build, asset budget and
  both-theme contrast checks passed; Go vet and Go tests passed against a
  disposable PostgreSQL, except three plugin tests that need `jq`/`python3`,
  absent from the test image. Overview, lists, details, Import and the command
  palette were checked in the browser at desktop and phone widths, in both
  themes, against the development stack; the readiness response was stubbed
  there because the running backend predates it. No resources were changed.

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
