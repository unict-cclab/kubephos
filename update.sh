#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }
source_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
install_dir="${HOME}/.local/share/kubephos"
[[ ! -f "$source_dir/.installer-managed" ]] || install_dir=$source_dir
pull_images=true
while (($#)); do
  case "$1" in
    --directory) (($# >= 2)) && [[ -n "$2" ]] || fail 'Missing installation directory.'; install_dir=$2; shift 2 ;;
    --no-pull) pull_images=false; shift ;;
    -h|--help) printf 'Usage: %s [--directory PATH] [--no-pull]\nApply the installed .env and update KubePhos from any working directory.\n' "$0"; exit 0 ;;
    *) fail "Unknown option: $1" ;;
  esac
done
[[ -d $install_dir && $install_dir != / ]] || fail 'Installation directory does not exist.'
install_dir=$(cd -- "$install_dir" && pwd)
[[ -f "$install_dir/.env" && -f "$install_dir/compose.yaml" ]] || fail 'The installation must contain .env and compose.yaml.'
command -v docker >/dev/null || fail 'Docker Engine and Compose are required.'
lock_dir="$install_dir/.install-lock"
mkdir -- "$lock_dir" 2>/dev/null || fail 'Another installation or update is in progress.'
trap 'rmdir -- "$lock_dir"' EXIT
log_file="$install_dir/update.log"
: > "$log_file"
chmod 600 "$log_file"
step() {
  local title=$1
  shift
  printf '%s\n' "$title"
  "$@" >> "$log_file" 2>&1 || fail "Update failed during: $title Details: $log_file"
}
compose_file="$install_dir/compose.yaml"
compose() (
  for key in ${!KUBEPHOS_@} POSTGRES_PASSWORD COMPOSE_PROJECT_NAME; do unset "$key"; done
  docker compose --project-directory "$install_dir" --env-file "$install_dir/.env" -f "$compose_file" "$@"
)
# Only this installation's application repository; never prune volumes, build
# caches, unrelated images, or anything referenced by any Docker container.
cleanup_release_images() {
  local image repository listing protected image_id reference _created previous_id=
  repository=$(sed -n 's/^KUBEPHOS_IMAGE=//p' "$install_dir/.env") || return 0
  repository=${repository#\"}; repository=${repository%\"}
  repository=${repository#\'}; repository=${repository%\'}
  repository=${repository:-ghcr.io/unict-cclab/kubephos}
  image=$(compose config --images app 2>> "$log_file" | while IFS= read -r candidate; do
    if [[ ${candidate%:*} == "$repository" && $candidate != *@* ]]; then printf '%s\n' "$candidate"; break; fi
  done) || return 0
  [[ -n $image ]] || return 0
  listing=$(docker image ls --no-trunc --format '{{.CreatedAt}}|{{.ID}}|{{.Repository}}:{{.Tag}}' "$repository" 2>> "$log_file" | sort -r) || return 0
  local containers
  local -a container_ids=()
  containers=$(docker ps -aq 2>> "$log_file") || return 0
  protected=
  if [[ -n $containers ]]; then
    mapfile -t container_ids <<< "$containers"
    protected=$(docker inspect --format '{{.Image}}' "${container_ids[@]}" 2>> "$log_file") || return 0
  fi
  while IFS='|' read -r _created image_id reference; do
    [[ -n $image_id && $reference == "$repository":* && $reference != *':<none>' ]] || continue
    if [[ $'\n'"$protected"$'\n' == *$'\n'"$image_id"$'\n'* ]]; then continue; fi
    if [[ -z $previous_id ]]; then previous_id=$image_id; fi
    [[ $image_id != "$previous_id" ]] || continue
    docker image rm "$reference" >> "$log_file" 2>&1 || true
  done <<< "$listing"
}
check_activity() {
  printf 'Checking running operations…\n'
  local active
  active=$(compose exec -T postgres psql -v ON_ERROR_STOP=1 -U kubephos -d kubephos -Atc "SELECT (SELECT count(*) FROM operations WHERE status IN ('queued','prechecking','running','verifying')) + (SELECT count(*) FROM pipeline_runs WHERE status='running' OR (status='queued' AND scheduled_for <= now())) + (SELECT count(*) FROM experiments WHERE status IN ('queued','initializing','initialized','running')) + (SELECT count(*) FROM plugin_import_jobs WHERE status IN ('queued','inspecting','validating','activating'))" 2>> "$log_file") || fail "Cannot verify running operations. Details: $log_file"
  [[ $active == 0 ]] || fail 'Wait for pending or running operations and imports to finish before updating.'
}
step 'Checking configuration…' compose config --quiet
check_activity
# Validate and download the release before replacing any installation files.
if [[ $source_dir != "$install_dir" && -f "$source_dir/compose.yaml" ]]; then
  compose_file="$source_dir/compose.yaml"
  step 'Checking release configuration…' compose config --quiet
fi
step 'Cleaning unused KubePhos images…' cleanup_release_images
if $pull_images; then step 'Downloading release images…' compose pull; fi
compose_file="$install_dir/compose.yaml"
check_activity
if [[ $source_dir != "$install_dir" && -f "$source_dir/compose.yaml" ]]; then
  cp -- "$install_dir/compose.yaml" "$install_dir/compose.previous.yaml"
  cp -- "$source_dir/compose.yaml" "$install_dir/compose.yaml"
  cp -- "$source_dir/update.sh" "$install_dir/update.sh"
  chmod 755 "$install_dir/update.sh"
fi
step 'Stopping the application and worker…' compose stop app worker
step 'Updating infrastructure services and database…' compose up -d --wait --wait-timeout 300 postgres keycloak migrate seaweedfs
step 'Synchronizing authentication…' compose run --rm --no-deps -T auth-sync
step 'Starting KubePhos…' compose up -d --wait --wait-timeout 180
step 'Checking service health…' compose exec -T app kubephos doctor
step 'Cleaning unused KubePhos images…' cleanup_release_images
printf 'KubePhos updated.\nInstallation: %s\nDetails: %s\n' "$install_dir" "$log_file"
