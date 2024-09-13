#!/usr/bin/env bash
set -euo pipefail

module="github.com/aalsanie/distroplane"
modules="$(go list -m all)"

if [ "$(printf '%s\n' "$modules" | sed '/^$/d' | wc -l | tr -d ' ')" -ne 1 ]; then
  echo "external Go module dependency detected:" >&2
  printf '%s\n' "$modules" >&2
  exit 1
fi

if ! printf '%s\n' "$modules" | grep -qx "$module"; then
  echo "unexpected root module list:" >&2
  printf '%s\n' "$modules" >&2
  exit 1
fi

external="$(go list -deps -f '{{if and (not .Standard) .Module}}{{if ne .Module.Path "github.com/aalsanie/distroplane"}}{{.ImportPath}}{{end}}{{end}}' ./... | sed '/^$/d' | sort -u)"
if [ -n "$external" ]; then
  echo "external Go package dependency detected:" >&2
  printf '%s\n' "$external" >&2
  exit 1
fi

echo "dependency budget satisfied: standard library only"
