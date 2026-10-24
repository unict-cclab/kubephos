#!/usr/bin/env bash
set -euo pipefail

# Release only the exact commit tested by the branch-push Checks workflow.
: "${GH_TOKEN:?GitHub token is required}"
: "${GITHUB_REPOSITORY:?Repository is required}"
: "${GITHUB_SHA:?Commit SHA is required}"

run_id=
for attempt in 1 2 3 4; do
  run_id=$(gh run list --repo "$GITHUB_REPOSITORY" --workflow checks.yml \
    --commit "$GITHUB_SHA" --event push --limit 1 \
    --json databaseId --jq '.[0].databaseId // empty')
  if [[ -n "$run_id" ]]; then break; fi
  if [[ "$attempt" != 4 ]]; then sleep 15; fi
done

if [[ -z "$run_id" ]]; then
  echo 'No Checks run exists for this commit. Push the commit to a branch before creating its release tag.' >&2
  exit 1
fi

printf 'Verifying Checks run %s for commit %s\n' "$run_id" "$GITHUB_SHA"
gh run watch "$run_id" --repo "$GITHUB_REPOSITORY" --exit-status --interval 15
