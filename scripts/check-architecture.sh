#!/usr/bin/env bash
set -euo pipefail

if grep -R --include='*.go' -nE '(^|\")github\.com/aalsanie/distroplane/(providers|internal/providers)(/|\")' internal cmd 2>/dev/null; then
  echo "core must not import provider implementation packages" >&2
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

echo "architecture guard satisfied"
