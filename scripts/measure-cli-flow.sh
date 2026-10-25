#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/project/dist"

# Warm build output is deliberately excluded from measured CLI invocations.
go build -trimpath -o "$work/bin/distroplane" "$repo/cmd/distroplane"
go build -trimpath -o "$work/bin/distroplane-provider-fake" "$repo/cmd/distroplane-provider-fake"
export PATH="$work/bin:$PATH"
printf 'controlled release artifact\n' > "$work/project/dist/app.bin"
cat > "$work/project/distroplane.json" <<'JSON'
{
  "schemaVersion": "1",
  "release": {
    "id": "benchmark-release",
    "artifacts": [{"name": "app", "source": "dist/app.bin"}]
  },
  "targets": [{
    "id": "primary",
    "provider": {"name": "fake"},
    "configuration": {"mode": "published"}
  }]
}
JSON

measure() {
  local label="$1"
  shift
  printf '\n%s\n' "$label"
  /usr/bin/time -p "$@" > /dev/null
}
echo "Platform: $(uname -s) $(uname -m)"
go version
measure "validate" "$work/bin/distroplane" validate --config "$work/project/distroplane.json" --json
measure "plan" "$work/bin/distroplane" plan --config "$work/project/distroplane.json" --json
plan="$(find "$work/project/.distroplane/plans" -maxdepth 1 -type f -name '*.json' -print -quit)"
if [[ -z "$plan" ]]; then
  echo 'plan was not created' >&2
  exit 1
fi
journal="$work/project/run.journal"
measure "apply" "$work/bin/distroplane" apply --config "$work/project/distroplane.json" --plan "$plan" --journal "$journal" --run run-measured --json
measure "status" "$work/bin/distroplane" status --plan "$plan" --journal "$journal" --json
measure "reconcile (completed)" "$work/bin/distroplane" reconcile --config "$work/project/distroplane.json" --plan "$plan" --journal "$journal" --json
measure "evidence" "$work/bin/distroplane" evidence --plan "$plan" --journal "$journal" --output "$work/evidence.json" --json
test -s "$work/evidence.json"
