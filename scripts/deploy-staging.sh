#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST="staging2"
VERSION=""
TIMEOUT="2h"

usage() {
  cat <<'USAGE'
Usage:
  scripts/deploy-staging.sh --version <version> [--host <ssh-alias>] [--timeout <duration>]

The incoming cm binary drains and waits for active background jobs before APT
installation or service restart. A timeout aborts the deployment.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      [[ $# -ge 2 && -n "$2" ]] || { echo "--version requires a value" >&2; exit 2; }
      VERSION="$2"
      shift 2
      ;;
    --host)
      [[ $# -ge 2 && -n "$2" ]] || { echo "--host requires a value" >&2; exit 2; }
      HOST="$2"
      shift 2
      ;;
    --timeout)
      [[ $# -ge 2 && -n "$2" ]] || { echo "--timeout requires a value" >&2; exit 2; }
      TIMEOUT="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[[ -n "$VERSION" ]] || { echo "--version is required" >&2; exit 2; }
[[ "$VERSION" =~ ^[0-9A-Za-z][0-9A-Za-z.+~-]*$ ]] || { echo "invalid version: $VERSION" >&2; exit 2; }
[[ "$HOST" =~ ^[0-9A-Za-z._@-]+$ ]] || { echo "invalid host alias: $HOST" >&2; exit 2; }
[[ "$TIMEOUT" =~ ^[1-9][0-9]*(ns|us|ms|s|m|h)$ ]] || { echo "invalid timeout: $TIMEOUT" >&2; exit 2; }

PACKAGE="${ROOT_DIR}/dist/cm_${VERSION}_arm64.deb"
[[ -f "$PACKAGE" ]] || { echo "package not found: $PACKAGE" >&2; exit 1; }

REMOTE_PACKAGE="/tmp/cm_${VERSION}_arm64.deb"
REMOTE_SHA=""
if command -v shasum >/dev/null 2>&1; then
  REMOTE_SHA="$(shasum -a 256 "$PACKAGE" | awk '{print $1}')"
else
  REMOTE_SHA="$(sha256sum "$PACKAGE" | awk '{print $1}')"
fi

echo "Uploading cm ${VERSION} to ${HOST}..."
scp "$PACKAGE" "${HOST}:${REMOTE_PACKAGE}"

echo "Waiting for ConnectMac background jobs before deployment..."
ssh "$HOST" sudo bash -s -- "$REMOTE_PACKAGE" "$REMOTE_SHA" "$VERSION" "$TIMEOUT" <<'REMOTE'
set -euo pipefail

package="$1"
expected_sha="$2"
version="$3"
timeout="$4"
preflight_dir="$(mktemp -d "/tmp/cm-${version}-preflight.XXXXXX")"
draining=0

cleanup() {
  status=$?
  if [[ $status -ne 0 && $draining -eq 1 && -x "$preflight_dir/usr/sbin/cm" ]]; then
    env HOME=/var/lib/connectmac "$preflight_dir/usr/sbin/cm" job end-drain || true
  fi
  rm -rf "$preflight_dir"
  exit "$status"
}
trap cleanup EXIT

actual_sha="$(sha256sum "$package" | awk '{print $1}')"
[[ "$actual_sha" == "$expected_sha" ]] || {
  echo "package checksum mismatch" >&2
  exit 1
}

dpkg-deb -x "$package" "$preflight_dir"
test -x "$preflight_dir/usr/sbin/cm"
draining=1
env HOME=/var/lib/connectmac "$preflight_dir/usr/sbin/cm" job wait-all --timeout "$timeout" --drain

apt install -y "$package"
systemctl restart connectmac
systemctl is-active --quiet connectmac
/usr/sbin/cm version

draining=0
REMOTE

echo "ConnectMac ${VERSION} deployed successfully to ${HOST}."
