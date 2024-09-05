#!/usr/bin/env bash
set -euo pipefail

version="${VERSION:-0.0.0-dev}"
commit="${COMMIT:-$(git rev-parse --short=12 HEAD 2>/dev/null || printf unknown)}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
out="${OUT_DIR:-dist}"

rm -rf "$out"
mkdir -p "$out"

ldflags="-s -w -X main.version=$version -X main.commit=$commit -X main.buildDate=$build_date"
targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
  "windows arm64"
)

for target in "${targets[@]}"; do
  read -r os arch <<<"$target"
  extension=""
  if [ "$os" = windows ]; then extension=".exe"; fi
  name="distroplane_${version}_${os}_${arch}${extension}"
  echo "building $name"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$ldflags" -o "$out/$name" ./cmd/distroplane
done

(
  cd "$out"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum distroplane_* > SHA256SUMS
  else
    shasum -a 256 distroplane_* > SHA256SUMS
  fi
)
