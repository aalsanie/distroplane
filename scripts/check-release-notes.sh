#!/usr/bin/env bash
set -euo pipefail

if [[ ! -f VERSION ]]; then
  echo "release notes check: VERSION file is missing" >&2
  exit 1
fi

version="$(tr -d '\r\n' < VERSION)"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]]; then
  echo "release notes check: invalid VERSION '$version'" >&2
  exit 1
fi

notes="docs/releases/v${version}.md"
if [[ ! -s "$notes" ]]; then
  echo "release notes check: missing or empty $notes" >&2
  exit 1
fi

heading="$(head -n 1 "$notes" | tr -d '\r')"
if [[ "$heading" != "# Distroplane v${version}" ]]; then
  echo "release notes check: first heading in $notes must be '# Distroplane v${version}'" >&2
  exit 1
fi

if ! awk 'NF { count++ } END { exit(count >= 8 ? 0 : 1) }' "$notes"; then
  echo "release notes check: $notes has insufficient content" >&2
  exit 1
fi

echo "release notes check passed: $notes"
