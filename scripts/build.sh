#!/usr/bin/env bash
set -euo pipefail

version="${VERSION:-0.0.0-dev}"
commit="${COMMIT:-$(git rev-parse --short=12 HEAD 2>/dev/null || printf unknown)}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
out="${OUT_DIR:-dist}"

rm -rf "$out"
mkdir -p "$out"

cli_ldflags="-s -w -X main.version=$version -X main.commit=$commit -X main.buildDate=$build_date"
provider_ldflags="-s -w -X main.version=$version"
targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
  "windows arm64"
)
providers=(npm sdkman)

for target in "${targets[@]}"; do
  read -r os arch <<<"$target"
  extension=""
  if [ "$os" = windows ]; then extension=".exe"; fi

  cli_name="distroplane_${version}_${os}_${arch}${extension}"
  echo "building $cli_name"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$cli_ldflags" -o "$out/$cli_name" ./cmd/distroplane

  for provider in "${providers[@]}"; do
    provider_name="distroplane-provider-${provider}_${version}_${os}_${arch}${extension}"
    echo "building $provider_name"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$provider_ldflags" -o "$out/$provider_name" "./cmd/distroplane-provider-${provider}"
  done
done

(
  cd "$out"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum distroplane* > SHA256SUMS
  else
    shasum -a 256 distroplane* > SHA256SUMS
  fi
)
