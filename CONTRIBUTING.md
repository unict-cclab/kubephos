# Contributing

KubePhos accepts bug reports, documentation improvements and pull requests.
For larger changes, open an issue first to discuss the intended behavior.

## Development

Follow [the development guide](docs/development.md). Keep changes focused and
include regression coverage for behavior changes. Run the applicable checks
before opening a pull request. Describe the problem, the resulting behavior
and what you verified.

Use the plugin contracts for new infrastructure integrations, applications and
execution strategies. Avoid application-specific branches in the platform core.
Preserve existing experiment snapshots and distinguish unavailable metrics from
zero measurements.

Keep field labels concise, include units, and reuse the shared form, table and
detail components. Verify changed screens in both light and dark mode.

Never include credentials, kubeconfigs, environment files, database dumps or
experiment artifacts in a pull request. Use synthetic fixtures for tests.
Published database changesets are immutable: add a new changeset for corrections.

By submitting a contribution, you agree to license your original contribution
under the project's MIT license. Preserve upstream copyright and license notices.
