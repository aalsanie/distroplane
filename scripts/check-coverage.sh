#!/usr/bin/env bash
set -euo pipefail

threshold="${COVERAGE_THRESHOLD:-90.0}"
profile="${COVERAGE_PROFILE:-coverage.out}"
packages_file="${COVERAGE_PACKAGES_FILE:-coverage-packages.txt}"

rm -f "$profile" "$packages_file"
go test ./... -covermode=atomic -coverprofile="$profile" | tee "$packages_file"

failed=0
while IFS= read -r line; do
  if [[ "$line" == ok*coverage:* ]]; then
    package="$(printf '%s\n' "$line" | awk '{print $2}')"
    percent="$(printf '%s\n' "$line" | sed -n 's/.*coverage: \([0-9.]*\)% of statements.*/\1/p')"
    if [ -n "$percent" ] && awk -v value="$percent" -v minimum="$threshold" 'BEGIN { exit !(value < minimum) }'; then
      printf 'coverage gate failed: %s = %s%%, required >= %s%%\n' "$package" "$percent" "$threshold" >&2
      failed=1
    fi
  elif [[ "$line" == \?*"[no test files]"* ]]; then
    package="$(printf '%s\n' "$line" | awk '{print $2}')"
    printf 'coverage gate failed: %s has no test files; required >= %s%%\n' "$package" "$threshold" >&2
    failed=1
  fi
done < "$packages_file"

total="$(go tool cover -func="$profile" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
if [ -z "$total" ]; then
  echo "coverage gate failed: could not determine repository coverage" >&2
  exit 1
fi

printf 'repository coverage: %s%% (required >= %s%%)\n' "$total" "$threshold"
if awk -v value="$total" -v minimum="$threshold" 'BEGIN { exit !(value < minimum) }'; then
  failed=1
fi

exit "$failed"
