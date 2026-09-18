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

protocol_provider_terms="$(grep -R --include='*.go' --exclude='*_test.go' -niE '(^|[^[:alnum:]_])(npm|sdkman|homebrew|formula|cask|tap|git|consumerKey|consumerToken|packagePath|dist-tag|updateBranch|pullRequest)([^[:alnum:]_]|$)' internal/protocol 2>/dev/null || true)"
if [ -n "$protocol_provider_terms" ]; then
  printf '%s\n' "$protocol_provider_terms" >&2
  echo "provider-specific vocabulary must not enter the core protocol" >&2
  exit 1
fi

echo "architecture guard satisfied"
