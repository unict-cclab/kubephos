#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

usage() {
  cat <<'HELP'
Usage: ./install.sh [--directory PATH] [--url URL] [--version TAG] [--admin-email EMAIL] [--prepare-only]

Install and start KubePhos on Linux using Docker Compose.
  --directory PATH  Installation directory (default: ~/.local/share/kubephos)
  --url URL         Browser URL (default: http://localhost:8080)
  --version TAG     Published image tag (default: latest)
  --admin-email EMAIL  Initial administrator email (prompted on first install)
  --prepare-only    Generate files without contacting Docker or starting services

Rerunning the installer resumes startup without rotating credentials or deleting data.
HELP
}
fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }
source_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
install_dir="${HOME}/.local/share/kubephos"
public_url=http://localhost:8080
release_version=latest
administrator_email=
prepare_only=false
requested_url=
requested_version=
while (($#)); do
  case "$1" in
    --directory|--url|--version|--admin-email)
      (($# >= 2)) && [[ -n "$2" ]] || fail "Missing value for $1"
      case "$1" in
        --directory) install_dir=$2 ;;
        --url) public_url=${2%/}; requested_url=$public_url ;;
        --version) release_version=$2; requested_version=$release_version ;;
        --admin-email) administrator_email=$2 ;;
      esac
      shift 2 ;;
    --prepare-only) prepare_only=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) fail "Unknown option: $1 (see --help)" ;;
  esac
done
[[ $(uname -s) == Linux ]] || fail 'This installer supports Linux.'
command -v openssl >/dev/null || fail 'Install OpenSSL first.'
[[ -f "$source_dir/compose.yaml" ]] || fail 'Keep install.sh beside the release compose.yaml.'
[[ $release_version =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$ ]] || fail 'Invalid image tag.'
[[ $public_url =~ ^https?://([a-zA-Z0-9.-]+|\[[a-fA-F0-9:]+\])(:[0-9]+)?$ ]] || fail 'URL must be an HTTP(S) origin without a path, query or credentials.'
public_host=${BASH_REMATCH[1],,}
[[ -n $install_dir && $install_dir != / ]] || fail 'Choose an installation directory.'
if ! $prepare_only; then
  command -v docker >/dev/null || fail 'Install Docker Engine and the Docker Compose plugin first.'
  compose_version=$(docker compose version --short) || fail 'Docker Compose is unavailable.'
  IFS=. read -r compose_major compose_minor compose_patch <<< "${compose_version#v}"
  compose_patch=${compose_patch%%-*}
  [[ $compose_major =~ ^[0-9]+$ && $compose_minor =~ ^[0-9]+$ && $compose_patch =~ ^[0-9]+$ ]] || fail 'Cannot determine the Docker Compose version.'
  ((compose_major > 2 || (compose_major == 2 && (compose_minor > 23 || (compose_minor == 23 && compose_patch >= 1))))) || fail 'Docker Compose 2.23.1 or newer is required.'
  docker info >/dev/null 2>&1 || fail 'Docker is unavailable. Check that it is running and your account can access it.'
fi
mkdir -p -- "$install_dir"
install_dir=$(cd -- "$install_dir" && pwd)
[[ $install_dir != "$source_dir" ]] || fail 'Use a separate installation directory, not the source checkout.'
# A lock prevents two installers from generating different credentials together.
lock_dir="$install_dir/.install-lock"
mkdir -- "$lock_dir" 2>/dev/null || fail "Another installation is in progress ($lock_dir)."
trap 'rmdir -- "$lock_dir"' EXIT
if [[ -e "$install_dir/.env" ]]; then
  [[ -f "$install_dir/.installer-managed" ]] || fail 'An existing .env was found. Existing installations are not overwritten.'
  [[ -f "$install_dir/credentials.json" && -f "$install_dir/compose.yaml" ]] || fail 'Installation files are incomplete; restore them from your backup.'
  saved_url=$(sed -n 's/^KUBEPHOS_URL=//p' "$install_dir/.env")
  saved_version=$(sed -n 's/^KUBEPHOS_VERSION=//p' "$install_dir/.env")
  saved_email=$(sed -n 's/.*"email"[[:space:]]*:[[:space:]]*"\([^"\]*\)".*/\1/p' "$install_dir/credentials.json")
  if [[ -n $requested_url && $requested_url != "$saved_url" ]] || [[ -n $requested_version && $requested_version != "$saved_version" ]] || [[ -n $administrator_email && $administrator_email != "$saved_email" ]]; then
    fail 'For an existing installation, edit its configuration explicitly. Credentials will not be regenerated.'
  fi
else
  [[ ! -e "$install_dir/compose.yaml" && ! -e "$install_dir/credentials.json" ]] || fail 'The destination already contains installation files.'
  if [[ -z $administrator_email ]]; then
    [[ -t 0 ]] || fail 'Use --admin-email EMAIL for a non-interactive installation.'
    read -r -p 'Administrator email: ' administrator_email
  fi
  [[ $administrator_email =~ ^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$ ]] || fail 'Enter a valid administrator email.'
  port=8080
  if [[ $public_url == http://* ]]; then port=80; fi
  bind_address=127.0.0.1
  authority=${public_url#*://}
  if [[ $public_url == http://* && $authority =~ :([0-9]+)$ ]]; then port=${BASH_REMATCH[1]}; fi
  if [[ ! $port =~ ^[1-9][0-9]{0,4}$ ]] || ((10#$port > 65535)); then fail 'URL port must be between 1 and 65535.'; fi
  if [[ $public_url == http://* ]]; then
    case "$public_host" in
      localhost) bind_address=127.0.0.1 ;;
      127.*) bind_address=$public_host ;;
      '[::1]') bind_address='[::1]' ;;
      '['*']') bind_address='[::]' ;;
      *) bind_address=0.0.0.0 ;;
    esac
  fi
  database_password=$(openssl rand -hex 32)
  client_secret=$(openssl rand -hex 32)
  management_secret=$(openssl rand -hex 32)
  bootstrap_password=$(openssl rand -hex 32)
  administrator_password=$(openssl rand -hex 24)
  # Commit the marker last. Interrupted preparation is reported rather than
  # silently replacing a secret that might already be in use.
  cp -- "$source_dir/compose.yaml" "$install_dir/compose.yaml"
  cat > "$install_dir/.env" <<ENV
# Generated by the KubePhos installer. Keep this file private.
COMPOSE_PROJECT_NAME=kubephos-$(openssl rand -hex 4)
KUBEPHOS_VERSION=$release_version
KUBEPHOS_URL=$public_url
KUBEPHOS_PORT=$port
KUBEPHOS_BIND_ADDRESS=$bind_address
POSTGRES_PASSWORD=$database_password
KUBEPHOS_KEYCLOAK_CLIENT_SECRET=$client_secret
KUBEPHOS_KEYCLOAK_MANAGEMENT_CLIENT_SECRET=$management_secret
KUBEPHOS_KEYCLOAK_ADMIN_PASSWORD=$bootstrap_password
ENV
  printf '{"username":"admin","email":"%s","password":"%s"}\n' "$administrator_email" "$administrator_password" > "$install_dir/credentials.json"
  touch "$install_dir/.installer-managed"
fi
chmod 600 "$install_dir/.env" "$install_dir/credentials.json"
# Never source .env as shell code. Compose parses it itself.
compose() (
  # Explicit installation files win over values exported by a development shell.
  while IFS='=' read -r key _; do
    case "$key" in KUBEPHOS_*|POSTGRES_PASSWORD|COMPOSE_PROJECT_NAME) unset "$key" ;; esac
  done < "$install_dir/.env"
  docker compose --project-directory "$install_dir" --env-file "$install_dir/.env" -f "$install_dir/compose.yaml" "$@"
)
if $prepare_only; then
  printf 'Installation files prepared in %s\n' "$install_dir"
  exit 0
fi
trap 'printf "Startup failed. Credentials and data were kept. Inspect services with: cd %q && docker compose ps -a\n" "$install_dir" >&2' ERR
compose config --quiet
compose pull
compose up -d --wait --wait-timeout 300 postgres keycloak migrate seaweedfs
# Provision the administrator before app/worker startup and port publication.
compose run --rm --no-deps -T app setup-admin < "$install_dir/credentials.json"
compose up -d --wait --wait-timeout 180
printf '\nKubePhos is running.\nInstallation: %s\nLogin credentials: %s/credentials.json\n' "$install_dir" "$install_dir"
printf 'URL: %s\n' "$(sed -n 's/^KUBEPHOS_URL=//p' "$install_dir/.env")"
