#!/usr/bin/env bash
set -euo pipefail

# Canonical release build script for XiT
# SOURCE -> RELEASE BUILD -> NPM ARTIFACT
# (Does NOT commit compiled binaries to git)

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# 1. Read current XiT version from npm/xitsg/package.json
VERSION=$(node -e 'console.log(require("./npm/xitsg/package.json").version)')
echo "Building XiT release version: ${VERSION}"

# 2. Create temporary staging
STAGING_DIR="/tmp/xit-release-${VERSION}"
NPM_STAGING="${STAGING_DIR}/npm"
rm -rf "${STAGING_DIR}"
mkdir -p "${NPM_STAGING}"

# 4. Copy tracked metadata and JS launcher from npm/xitsg/
cp "${REPO_ROOT}/npm/xitsg/package.json" "${NPM_STAGING}/"
cp "${REPO_ROOT}/npm/xitsg/README.md" "${NPM_STAGING}/"
cp "${REPO_ROOT}/npm/xitsg/LICENSE" "${NPM_STAGING}/"
cp -r "${REPO_ROOT}/npm/xitsg/bin" "${NPM_STAGING}/"

# 5. Platforms to build: darwin-arm64, darwin-amd64, linux-arm64, linux-amd64, win32-amd64
PLATFORMS=(
  "darwin/arm64/darwin-arm64/xit"
  "darwin/amd64/darwin-amd64/xit"
  "linux/arm64/linux-arm64/xit"
  "linux/amd64/linux-amd64/xit"
  "windows/amd64/win32-amd64/xit.exe"
)

PRODUCTION_API="https://xit-api.stephenwilson.dev"
LDFLAGS="-X github.com/stephenywilson/xit/internal/apibase.Default=${PRODUCTION_API}"

echo "Building release binaries with injected production API Base: ${PRODUCTION_API}"

mkdir -p "${NPM_STAGING}/vendor"

for item in "${PLATFORMS[@]}"; do
  IFS="/" read -r GOOS GOARCH OUTDIR OUTBIN <<< "$item"
  TARGET_DIR="${NPM_STAGING}/vendor/${OUTDIR}"
  TARGET_PATH="${TARGET_DIR}/${OUTBIN}"
  mkdir -p "${TARGET_DIR}"

  echo "Compiling ${GOOS}/${GOARCH} -> ${TARGET_PATH}..."
  GOOS="${GOOS}" GOARCH="${GOARCH}" go build -ldflags "${LDFLAGS}" -o "${TARGET_PATH}" ./cmd/xit

  SHA=$(shasum -a 256 "${TARGET_PATH}" | awk '{print $1}')
  echo "  version:     ${VERSION}"
  echo "  platform:    ${OUTDIR}"
  echo "  binary path: ${TARGET_PATH}"
  echo "  sha256:      ${SHA}"
done

echo ""
echo "Release build complete!"
echo "Staging directory: ${NPM_STAGING}"
