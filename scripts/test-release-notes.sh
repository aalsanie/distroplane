#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
validator="$root/scripts/check-release-notes.sh"
sandbox="$(mktemp -d)"
trap 'rm -rf "$sandbox"' EXIT
cd "$sandbox"
mkdir -p docs/releases
printf '0.9.0\n' > VERSION
cat > docs/releases/v0.9.0.md <<'EOF'
# Distroplane v0.9.0

Release summary.

## Changes

One change.
Second change.
Third change.

## Compatibility

Compatibility details.
EOF

"$validator" >/dev/null

expect_failure() {
  if "$validator" >/dev/null 2>&1; then
    echo "release notes validator accepted an invalid fixture: $1" >&2
    exit 1
  fi
}

mv docs/releases/v0.9.0.md docs/releases/v0.9.0.md.bak
expect_failure "missing release notes"
mv docs/releases/v0.9.0.md.bak docs/releases/v0.9.0.md

sed -i.bak '1s/v0\.9\.0/v0.9.1/' docs/releases/v0.9.0.md
expect_failure "mismatched version heading"
mv docs/releases/v0.9.0.md.bak docs/releases/v0.9.0.md

printf '# Distroplane v0.9.0\n' > docs/releases/v0.9.0.md
expect_failure "insufficient release notes"

printf 'bad-version\n' > VERSION
expect_failure "invalid VERSION"
rm VERSION
expect_failure "missing VERSION"

echo "release notes validator fixture tests passed"
