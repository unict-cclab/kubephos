# Frontend quality

Use the existing local React components and theme tokens. PatternFly's
[form](https://www.patternfly.org/components/forms/form/design-guidelines/) and
[wizard](https://www.patternfly.org/components/wizard/design-guidelines/) guidance
is the interaction reference; the PatternFly library is not a dependency.

## Shared conventions

- Apply fixes in shared components and verify their consuming screens.
- Keep labels short and bold, values regular, and units in field labels.
- Use label/value tables for details; show authorship and dates before resource tabs.
- Keep related fields together and descriptions limited to decisions users must make.
- Preserve form values across wizard steps, tabs and asynchronous suggestions.
- Allow direct step navigation; validate every step before saving and focus invalid fields.
- Keep row actions visible at narrow widths and sorting available in Table display.
- Preserve private defaults, ownership and read-only sharing when changing presentation.
- Show actionable errors; keep raw JSON, SQL, network traces and credentials out of messages.

## Automated checks

From the repository root, with Node 24.20 as used by CI:

```bash
npm --prefix frontend ci
npm --prefix frontend test
npm --prefix frontend run build
npm --prefix frontend run check:bundle
npm --prefix frontend run check:theme
npm --prefix frontend audit --audit-level=moderate
```

The suite includes keyboard navigation, schema values, failed saves, sharing,
resource details, statistics and exports. Focused checks are available as
`test:usability` and `test:a11y`.

The accessibility tests use axe-core with WCAG A/AA rules supported by jsdom.
The theme check covers representative text and control color pairs in both themes.
The initial JavaScript/CSS budget is 220 KiB gzip. These checks do not replace
browser layout, assistive-technology testing or sessions with representative users.

## Browser review

Review the actual build in light and dark modes at desktop, tablet and phone
widths. Cover Overview, Infrastructure, Kubernetes, Catalog, Configurations,
Experiments/results, Users and Administration, including creation, details,
empty/error states and wizard navigation. Do not submit provisioning, deletions,
credential changes, sharing changes or experiments during a visual walkthrough.
Record the completed review and its limits in [the repository review](../docs/review.md).
