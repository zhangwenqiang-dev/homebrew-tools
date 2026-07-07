#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
VERSION="${VERSION:-}"
ARCH="${ARCH:-}"
PACKAGE="cm"
MAINTAINER="${MAINTAINER:-ConnectMac <connectmac@example.com>}"

usage() {
  cat <<'USAGE'
Usage:
  scripts/build-deb.sh [--version <version>] [--arch <amd64|arm64>] [--all]

Environment:
  VERSION     Package version. Defaults to latest git tag without leading v.
  ARCH        Debian architecture for single-arch build.
  MAINTAINER  Debian maintainer field.

Examples:
  scripts/build-deb.sh --version 0.1.76 --arch arm64
  scripts/build-deb.sh --version 0.1.76 --all
USAGE
}

ALL=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --arch)
      ARCH="${2:-}"
      shift 2
      ;;
    --all)
      ALL=1
      shift
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

if [[ -z "${VERSION}" ]]; then
  VERSION="$(git -C "${ROOT_DIR}" describe --tags --abbrev=0 2>/dev/null | sed 's/^v//')"
fi
if [[ -z "${VERSION}" ]]; then
  VERSION="dev"
fi

host_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *) echo "unsupported" ;;
  esac
}

go_arch_for_deb() {
  case "$1" in
    amd64) echo "amd64" ;;
    arm64) echo "arm64" ;;
    *) return 1 ;;
  esac
}

build_one() {
  local deb_arch="$1"
  local go_arch
  go_arch="$(go_arch_for_deb "${deb_arch}")" || {
    echo "unsupported architecture: ${deb_arch}" >&2
    exit 2
  }

  local work
  work="$(mktemp -d "${TMPDIR:-/tmp}/cm-deb.XXXXXX")"
  trap 'rm -rf "${work}"' RETURN

  local stage="${work}/stage"
  local control="${work}/control"
  local binary="${stage}/usr/sbin/cm"
  local deb="${DIST_DIR}/${PACKAGE}_${VERSION}_${deb_arch}.deb"

  mkdir -p \
    "${stage}/usr/bin" \
    "${stage}/usr/sbin" \
    "${stage}/usr/share/connectmac" \
    "${stage}/usr/share/doc/cm" \
    "${stage}/var/lib/connectmac" \
    "${stage}/etc/connectmac" \
    "${stage}/lib/systemd/system" \
    "${control}"

  echo "Building ${PACKAGE} ${VERSION} for linux/${go_arch}..."
  (
    cd "${ROOT_DIR}"
    CGO_ENABLED=0 GOOS=linux GOARCH="${go_arch}" go build \
      -ldflags "-X main.version=${VERSION}" \
      -o "${binary}" \
      ./cmd/cm
  )

  chmod 0755 "${binary}"
  ln -s ../sbin/cm "${stage}/usr/bin/cm"
  cp -R "${ROOT_DIR}/web" "${stage}/usr/share/connectmac/web"
  cp -R "${ROOT_DIR}/web" "${stage}/var/lib/connectmac/web"
  cp "${ROOT_DIR}/README.md" "${stage}/usr/share/doc/cm/README.md"
  gzip -9 -c "${ROOT_DIR}/LICENSE" > "${stage}/usr/share/doc/cm/copyright.gz"

  cat > "${stage}/lib/systemd/system/connectmac.service" <<'SERVICE'
[Unit]
Description=ConnectMac Web Manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=-/etc/connectmac/.env
ExecStart=/usr/sbin/cm web --host 127.0.0.1 --port 18080 --web-dir /var/lib/connectmac/web
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
SERVICE

  cat > "${control}/control" <<CONTROL
Package: ${PACKAGE}
Version: ${VERSION}
Section: utils
Priority: optional
Architecture: ${deb_arch}
Maintainer: ${MAINTAINER}
Homepage: https://github.com/zhangwenqiang-dev/homebrew-tools
Description: SSH, VNC, rsync, and AWS Mac profile manager
 ConnectMac provides the cm command for managing SSH tunnels, VNC access,
 rsync transfer profiles, web management, and AWS Mac Dedicated Host workflows.
CONTROL

  cat > "${control}/postinst" <<'POSTINST'
#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
exit 0
POSTINST
  chmod 0755 "${control}/postinst"

  cat > "${control}/postrm" <<'POSTRM'
#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
exit 0
POSTRM
  chmod 0755 "${control}/postrm"

  mkdir -p "${DIST_DIR}"
  (
    cd "${control}"
    COPYFILE_DISABLE=1 tar --format=ustar --uid 0 --gid 0 --numeric-owner -czf "${work}/control.tar.gz" .
  )
  (
    cd "${stage}"
    COPYFILE_DISABLE=1 tar --format=ustar --uid 0 --gid 0 --numeric-owner -czf "${work}/data.tar.gz" .
  )
  printf '2.0\n' > "${work}/debian-binary"

  rm -f "${deb}"
  (
    cd "${work}"
    ar -qcS "${deb}" debian-binary control.tar.gz data.tar.gz
  )
  echo "Wrote ${deb}"
}

if [[ "${ALL}" == "1" ]]; then
  build_one amd64
  build_one arm64
else
  if [[ -z "${ARCH}" ]]; then
    ARCH="$(host_arch)"
  fi
  if [[ "${ARCH}" == "unsupported" ]]; then
    echo "unsupported host architecture; pass --arch amd64 or --arch arm64" >&2
    exit 2
  fi
  build_one "${ARCH}"
fi
