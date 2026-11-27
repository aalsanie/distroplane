#!/usr/bin/env bash
set -euo pipefail

if grep -R --include='*.go' -nE '(^|\")github\.com/aalsanie/distroplane/(providers|internal/providers)(/|\")' internal cmd/distroplane 2>/dev/null; then
  echo "core must not import provider implementation packages" >&2
  exit 1
fi

provider_core_imports="$(grep -R --include='*.go' -nE '"github\.com/aalsanie/distroplane/internal/' providers 2>/dev/null | grep -v '"github\.com/aalsanie/distroplane/internal/protocol"' || true)"
if [ -n "$provider_core_imports" ]; then
  printf '%s\n' "$provider_core_imports" >&2
  echo "providers must depend only on the protocol internal package" >&2
  exit 1
fi

if grep -R --include='*.go' -nE '"github\.com/aalsanie/distroplane/internal/(config|fakeprovider|planner|protocol)"' internal/executor 2>/dev/null; then
  echo "executor must depend only on domain and journal internal packages" >&2
  exit 1
fi

if grep -R --include='*.go' -nE '"github\.com/aalsanie/distroplane/internal/fakeprovider"' internal/providerhost 2>/dev/null; then
  echo "provider host must not import provider implementations" >&2
  exit 1
fi

if grep -R --include='*.go' -nE '"github\.com/aalsanie/distroplane/internal/(config|executor|fakeprovider|journal|planner|protocol|providerhost)"' internal/credentials 2>/dev/null; then
  echo "credentials must depend only on domain internal package" >&2
  exit 1
fi

if grep -R --include='*.go' -nE '"github\.com/aalsanie/distroplane/internal/(config|credentials|executor|fakeprovider|planner|protocol|providerhost)"' internal/evidence 2>/dev/null; then
  echo "evidence must depend only on canonicaljson, domain, and journal internal packages" >&2
  exit 1
fi

protocol_provider_terms="$(grep -R --include='*.go' --exclude='*_test.go' -niE '(^|[^[:alnum:]_])(npm|sdkman|homebrew|winget|formula|cask|tap|git|consumerKey|consumerToken|packagePath|dist-tag|updateBranch|pullRequest|PackageIdentifier|InstallerSha256|ManifestType|ManifestVersion|defaultLocale)([^[:alnum:]_]|$)' internal/protocol 2>/dev/null || true)"
if [ -n "$protocol_provider_terms" ]; then
  printf '%s\n' "$protocol_provider_terms" >&2
  echo "provider-specific vocabulary must not enter the core protocol" >&2
  exit 1
fi

kernel=(
  internal/canonicaljson internal/config internal/credentials internal/domain
  internal/evidence internal/executor internal/journal internal/planner
  internal/protocol internal/providerhost internal/providerretention internal/runstate
)
kernel_packages=()
for package in "${kernel[@]}"; do
  kernel_packages+=("./$package")
done
dependencies="$(go list -deps -f '{{.ImportPath}}' "${kernel_packages[@]}")"
if grep -E '^github\.com/aalsanie/distroplane/(internal/frontend|cmd|providers)(/|$)' <<< "$dependencies"; then
  echo "kernel dependency closure must not include frontend or provider implementations" >&2
  exit 1
fi

kernel_provider_terms="$(grep -R --include='*.go' --exclude='*_test.go' --exclude='legacy_v2_retry.go' -niE '(^|[^[:alnum:]_])(npm|sdkman|homebrew|winget|packagePath|archivePath|DISTROPLANE_NPM_TOKEN|DISTROPLANE_SDKMAN_VENDOR_EMAIL|DISTROPLANE_HOMEBREW_TOKEN|DISTROPLANE_WINGET_TOKEN)([^[:alnum:]_]|$)' "${kernel[@]}" || true)"
if [ -n "$kernel_provider_terms" ]; then
  printf '%s\n' "$kernel_provider_terms" >&2
  echo "official-provider interpretation belongs outside the kernel" >&2
  exit 1
fi

echo "architecture guard satisfied"
