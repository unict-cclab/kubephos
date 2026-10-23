# Frontend design and quality checks

## Design reference

Use [PatternFly form guidance](https://www.patternfly.org/components/forms/form/design-guidelines/) and [wizard guidance](https://www.patternfly.org/components/wizard/design-guidelines/) as the reference for interaction and layout. The frontend uses shared local React components; it does not import the PatternFly component library.

- Apply shared fixes in the common component or layout primitive, then verify every consuming flow. Keep only resource-specific composition local.
- Keep wizard navigation separate from the content on desktop; use the step selector on small screens.
- Keep names and destinations readable. Pair related fields, not arbitrary consecutive fields.
- Size short values such as VM IDs, node counts and network prefixes accordingly.
- Align controls at the top, including when an adjacent field has helper text.
- Keep inactive steps and tabs mounted so navigation cannot discard configuration values.
- Use `FormDisclosure` for optional editor sections. Open saved overrides initially and preserve the user’s expanded/collapsed choice.
- Declare `x-kubephos-order` on schema properties when field order matters; use the shared schema renderer instead of application-specific ordering.
- Keep additive actions labeled across editors; reserve icon-only controls for familiar row actions.
- Keep workload, traffic distribution, generator settings and preview in the shared load editor’s separate panels.
- Use one primary action and a consistent footer. Validate before advancing and focus the invalid field, opening every enclosing tab and disclosure when needed. Use wizard step status messages for asynchronous checks so errors cannot remain hidden in another tab.
- Show units in numeric field labels consistently in shared profiles and individual overrides; retain the backend units in submitted values. Use the shared form stack and disclosure spacing for nested editors.
- Keep descriptions limited to information needed for the user's decision.
- Preserve author attribution and access controls when changing presentation.

## Repeatable checks

Run from the repository root:

```sh
npm --prefix frontend ci
npm --prefix frontend test
npm --prefix frontend run build
npm --prefix frontend run check:bundle
npm --prefix frontend run check:theme
npm --prefix frontend audit --audit-level=moderate
```

Focused checks:

```sh
npm --prefix frontend run test:usability
npm --prefix frontend run test:a11y
```

The CI frontend job runs the full test suite, production build, asset budget, theme contrast checks and dependency audit. Use Node 24.20 or another version supported by the installed tooling.

## What the checks cover

The full suite covers frontend behavior and request contracts, including cluster provisioning payloads, schema values, configuration and experiment actions, sharing permissions, resource attribution, result statistics, plots and export controls.

Task-based interaction tests cover keyboard navigation, invalid fields in hidden tabs, returning to previous steps, compact step selection, multiline input, preservation of dynamically added cluster members, duplicate submission prevention and recovery after a save error.

The axe-core audit covers wizard steps and tabs, table display options, search, empty results, the actual sharing dialog, workload timelines and chart preview, schema fields, advanced settings and service resource overrides. It checks WCAG A/AA rules supported by jsdom. Browser layout and canvas-dependent color contrast are outside this audit.

The theme check verifies 16 representative text, action, status, input boundary and focus color pairs across light and dark themes. Text requires 4.5:1; input boundaries and focus indicators require 3:1. This is a regression check for palette pairs, not a claim of complete WCAG conformance.

The asset check limits the initial JavaScript and CSS referenced by the production HTML to 220 KiB gzip. It is a size budget, not a browser loading-time measurement.

## Browser walkthrough — 20 October 2026

Checked in the in-app browser using the authenticated application and its local preview:

- Single and multi cluster creation, member pools, networking and VM placement.
- VM template, Harbor and NFS creation layouts.
- Experiment configuration steps, application settings and resource fields.
- Required-field validation, Enter to advance, navigation back without losing values.
- Table search, display options and sorting, with no resource changes submitted.
- Desktop, 820px and 390px layouts; compact navigation and resource tables on small screens.
- Light and dark themes.

At 390px the configuration wizard document width and scroll width matched, with no horizontal page overflow. Resource tables switch to stacked rows, keeping authors and actions available; sorting remains available in table display settings.

These are developer walkthroughs and simulated task tests. They do not replace usability sessions with representative users, assistive-technology testing, or a Firefox/Safari browser matrix. Provisioning real VMs and launching real experiments are not part of this frontend verification.

## Review on 20 October 2026

The browser review covered Overview; Infrastructure connections, templates, Harbor and NFS; Kubernetes details, configuration, node maintenance and single/multi-cluster creation; Catalog applications, schedulers, deschedulers, autoscalers and AI models; saved configuration details and readout; experiment creation, sharing and results; Users list, detail and creation. Provisioning, account creation, sharing changes and experiment execution were not submitted during visual checks.

The latest saved experiment was used to inspect Statistics, service groups, Plots and Export. Responsive checks cover narrow viewports and preserve all statistics without horizontal scrolling. Automated checks exercise metric aggregation, CSV export, missing control metrics, explicit CPU units, small axis ticks, natural service ordering, service filtering, schema-based readout labels, secret masking and keyboard navigation. These are agent-led checks, not a usability study with human participants.

Read-only data uses the shared `InputData` layout: structured values span the available width, scalar lists do not create numbered nested boxes, multiline configuration remains readable, and schema titles/order are reused. Results and Users share `ResponsiveTable` for stacked narrow-screen rows. Forms use the shared stack and disclosure spacing.

21 October follow-up: shared detail readouts use label/value rows, with attribution before resource tabs and consistent Created/Created by/Last updated/Last updated by labels. Structured workload and traffic phases retain matrix tables. The final Continue control now has a distinct React key and cancels its default action so changing it into Save cannot trigger a browser submission. A regression verifies the button is replaced and review does not submit. During agent-led QA, the earlier bug submitted the open baseline configuration without input edits; its modification timestamp changed. The saved workload and traffic values match the values observed before that navigation. No experiment was started.

Infrastructure section review (21 October): all four resource lists use Name, expose Last updated by, and identify their Proxmox connection where applicable. Provider is omitted from the Proxmox-only connection table. Template image URL is on the first wizard step; schema labels use Cloud image URL and Cloud image storage with no DHCP helper sentence. Harbor/NFS use Resources. Shared detail labels are bold, values regular, and table cells have row/column separators. Browser checks covered the connection detail, all four lists, visible template URL and both service creation wizards without provisioning or saving credentials; the eight-column NFS list fits the standard viewport without horizontal overflow.

Typography follow-up: detail field names, attribution labels and table headers use the normal foreground with bold weight; values use normal foreground at regular weight, including resource names in table cells. Template schema wording is aligned in both the packaged manifest and runtime descriptor.

Users follow-up: Email is a dedicated column. Shared list headers, row cells and display options follow Created → Created by → Last updated → Last updated by; detail attribution follows the same order. Additional checks cover the column order and the email cell. Status/role cells use regular value weight, and author names/role labels stay on one line.

21 October standardization review: a shared field-label map now drives Name, Image, Configuration, Resources, Pools and common time/resource units in schema forms and readouts. Template and infrastructure services start on General; Harbor/NFS follow General → Resources → Network → Review. Catalog applications, strategy categories and models use ResourceListTable; model details support direct URLs and read-only access. Application services use a labeled table. Strategy configurations are disclosed separately from scalar defaults. Shared readout tables size to content; field/value label columns are capped. Form spacing uses container gaps without additive child margins; phase selectors have visible Type labels and Phase identifiers.

Network review: correlation is removed from the editor/readout and serialized as zero for shared profiles; directed correlation overrides are omitted on save. Jitter remains. Every link readout includes latency, jitter, bandwidth and loss, using an em dash for unset values and inheriting enabled shared values. Link editing uses a responsive table with headers and advanced target fields after the links. Agent-led browser checks traversed Monitoring, Load profile, Scheduling and Review without saving, confirmed Review waited for an explicit Save, verified computed first-column padding/weights and zero additive component margins, and checked catalog navigation and model details. These are automated/agent-led checks, not a human usability study.

Final detail-cell verification also found an inherited 5 px bottom margin on the label spans in summary readouts; removing it restores continuous vertical separators. The browser verification uses a full page reload for the final CSS, since hot refresh retained an older stylesheet during part of the preview.
