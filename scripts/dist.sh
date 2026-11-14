#!/usr/bin/env bash
#
# Builds the FleetPulse exporter release binaries and dist/SHA256SUMS.
#
# VERSION   version stamped into the binaries and used in artifact names
#           (default: 0.1.0)
# DIST_DIR  output directory (default: <repo>/dist)
# GO        go binary to use (default: go)
#
# Example: VERSION=0.2.0 make dist
set -euo pipefail

VERSION="${VERSION:-0.1.0}"
VERSION="${VERSION//\//-}"
VERSION="${VERSION// /-}"
GO="${GO:-go}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
DIST_DIR="${DIST_DIR:-${ROOT_DIR}/dist}"

targets=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
)

rm -rf "${DIST_DIR}"
mkdir -p "${DIST_DIR}"

cd "${ROOT_DIR}"

artifacts=()
for target in "${targets[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  name="fleetpulse-exporter_${VERSION}_${goos}_${goarch}"
  if [ "${goos}" = "windows" ]; then
    name="${name}.exe"
  fi

  echo "building ${name}"
  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
    "${GO}" build \
    -trimpath \
    -ldflags "-s -w -X fleetpulse/internal/version.Version=${VERSION}" \
    -o "${DIST_DIR}/${name}" \
    ./cmd/exporter
  artifacts+=("${name}")
done

if command -v sha256sum >/dev/null 2>&1; then
  (cd "${DIST_DIR}" && sha256sum "${artifacts[@]}" >SHA256SUMS)
else
  (cd "${DIST_DIR}" && shasum -a 256 "${artifacts[@]}" >SHA256SUMS)
fi

echo "wrote ${DIST_DIR}/SHA256SUMS"
cat "${DIST_DIR}/SHA256SUMS"
