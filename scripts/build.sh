#!/usr/bin/env bash
# build.sh — cross-compile myself-ai for all target platforms
set -euo pipefail

BINARY="myself-ai"
DIST_DIR="dist"
PKG="github.com/1344011985/MyselfAI/internal/command"
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-X ${PKG}.GitCommit=${COMMIT} -X ${PKG}.BuildDate=${DATE}"

PLATFORMS=(
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
  "darwin/amd64"
  "darwin/arm64"
)

mkdir -p "${DIST_DIR}"

for platform in "${PLATFORMS[@]}"; do
  OS="${platform%%/*}"
  ARCH="${platform##*/}"
  OUTPUT="${DIST_DIR}/${BINARY}-${OS}-${ARCH}"

  if [ "${OS}" = "windows" ]; then
    OUTPUT="${OUTPUT}.exe"
  fi

  echo "Building ${OUTPUT} ..."
  GOOS="${OS}" GOARCH="${ARCH}" go build -ldflags "${LDFLAGS}" -o "${OUTPUT}" ./cmd/bot/

  # Package
  if [ "${OS}" = "windows" ]; then
    zip -j "${DIST_DIR}/${BINARY}-${OS}-${ARCH}.zip" "${OUTPUT}"
  else
    tar -czf "${DIST_DIR}/${BINARY}-${OS}-${ARCH}.tar.gz" -C "${DIST_DIR}" "${BINARY}-${OS}-${ARCH}"
  fi
done

echo "Done. Artifacts in ${DIST_DIR}/"
