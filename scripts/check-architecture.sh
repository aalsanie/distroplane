#!/usr/bin/env bash
set -euo pipefail

if grep -R --include='*.go' -nE '(^|\")github\.com/aalsanie/distroplane/(providers|internal/providers)(/|\")' internal cmd 2>/dev/null; then
  echo "core must not import provider implementation packages" >&2
  exit 1
fi

echo "architecture guard satisfied"
