#!/usr/bin/env bash
set -euo pipefail

version="${1:?usage: update-homebrew-tap.sh VERSION}"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo 'tap update requires a stable release version' >&2
  exit 1
fi
: "${SOURCE_TOKEN:?SOURCE_TOKEN is required to inspect the published release}"
: "${TAP_TOKEN:?HOMEBREW_TAP_TOKEN is required to submit a reviewed tap PR}"
source_repo='aalsanie/distroplane'
tap_repo='aalsanie/homebrew-tap'
tag="v$version"
branch="update/distroplane-v$version"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/assets"

release="$(GH_TOKEN="$SOURCE_TOKEN" gh api "repos/$source_repo/releases/tags/$tag")"
if ! jq -e '.draft == false and .prerelease == false and .published_at != null' <<<"$release" >/dev/null; then
  echo "published stable release $tag not found" >&2
  exit 1
fi
GH_TOKEN="$SOURCE_TOKEN" gh release download "$tag" --repo "$source_repo" --dir "$work/assets" \
  --pattern 'RELEASE-METADATA.json' --pattern 'SHA256SUMS' \
  --pattern "distroplane_${version}_darwin_*.tar.gz" \
  --pattern "distroplane_${version}_linux_*.tar.gz"
python3 scripts/homebrew_formula.py --release-dir "$work/assets" --version "$version" --output "$work/distroplane.rb"

source_sha="$(GH_TOKEN="$SOURCE_TOKEN" gh api "repos/$source_repo/commits/$tag" --jq '.sha')"
SOURCE_SHA="$source_sha" RELEASE_METADATA="$work/assets/RELEASE-METADATA.json" python3 - <<'PY'
import json, os, re
with open(os.environ['RELEASE_METADATA'], encoding='utf-8') as stream:
    commit = json.load(stream)['commit']
sha = os.environ['SOURCE_SHA']
if not re.fullmatch(r'[0-9a-f]{40}', sha) or sha[:12] != commit:
    raise SystemExit('published release artifacts do not match the tagged source commit')
PY

api() { GH_TOKEN="$TAP_TOKEN" gh api "$@"; }
# Only an HTTP 404 means that a GitHub resource is absent. Permission,
# transport, and server errors must never be mistaken for a missing formula.
read_optional() {
  local response error_status
  if response="$(api "$1" 2>"$work/github-error")"; then
    printf '%s\n' "$response"
    return 0
  fi
  if grep -Eq '\(HTTP 404\)' "$work/github-error"; then
    return 0
  fi
  echo "unable to inspect tap resource $1; refusing to proceed" >&2
  return 1
}
main_sha="$(api "repos/$tap_repo/git/ref/heads/main" --jq '.object.sha')"
formula_path='Formula/distroplane.rb'
main_record="$(read_optional "repos/$tap_repo/contents/$formula_path?ref=main")"
if [[ -n "$main_record" ]]; then
  jq -er '.content' <<<"$main_record" | base64 --decode > "$work/current.rb"
  python3 - "$work/current.rb" "$work/distroplane.rb" <<'PY'
import pathlib, re, sys
old, new = [pathlib.Path(a).read_text(encoding='utf-8') for a in sys.argv[1:]]
def formula_version(source):
    pattern = (r'^\s*url "https://github\.com/aalsanie/distroplane/releases/download/'
               r'v([0-9]+\.[0-9]+\.[0-9]+)/distroplane_\1_(darwin|linux)_(arm64|amd64)\.tar\.gz"
if old_v > new_v or (old_v == new_v and old != new):
    raise SystemExit('tap formula differs at same/newer version; refusing downgrade or overwrite')
PY
  if cmp -s "$work/current.rb" "$work/distroplane.rb"; then
    echo "tap formula for $tag already published; no change"
    exit 0
  fi
fi

branch_record="$(read_optional "repos/$tap_repo/git/ref/heads/$branch")"
write_formula() {
  local content
  content="$(base64 < "$work/distroplane.rb" | tr -d '\n')"
  api --method PUT "repos/$tap_repo/contents/$formula_path" \
    -f "message=chore: update Distroplane Homebrew formula to $tag" \
    -f "content=$content" -f "branch=$branch" "$@" >/dev/null
}
if [[ -n "$branch_record" ]]; then
  existing_record="$(read_optional "repos/$tap_repo/contents/$formula_path?ref=$branch")"
  if [[ -z "$existing_record" ]]; then
    branch_sha="$(jq -er '.object.sha' <<<"$branch_record")"
    if [[ "$branch_sha" != "$main_sha" ]]; then
      echo 'existing tap branch differs from main and lacks a formula; review required' >&2
      exit 1
    fi
    write_formula
  else
    jq -er '.content' <<<"$existing_record" | base64 --decode > "$work/branch.rb"
    if ! cmp -s "$work/branch.rb" "$work/distroplane.rb"; then
      echo 'tap update branch has conflicting upstream changes; review required' >&2
      exit 1
    fi
  fi
else
  actual_sha="$(api "repos/$tap_repo/git/ref/heads/main" --jq '.object.sha')"
  if [[ "$actual_sha" != "$main_sha" ]]; then
    echo 'tap main changed while preparing update; rerun after review' >&2
    exit 1
  fi
  api --method POST "repos/$tap_repo/git/refs" -f "ref=refs/heads/$branch" -f "sha=$main_sha" >/dev/null
  sha_arg=()
  if [[ -n "$main_record" ]]; then
    sha_arg+=( -f "sha=$(jq -er '.sha' <<<"$main_record")" )
  fi
  write_formula "${sha_arg[@]}"
fi

prs="$(api "repos/$tap_repo/pulls?state=all&head=aalsanie:$branch&per_page=50")"
if jq -e 'any(.[]; .state == "closed")' <<<"$prs" >/dev/null; then
  echo 'tap update PR was previously closed; human review required' >&2
  exit 1
fi
if jq -e 'any(.[]; .state == "open")' <<<"$prs" >/dev/null; then
  echo "existing reviewed tap update PR for $tag; no duplicate created"
  exit 0
fi
api --method POST "repos/$tap_repo/pulls" \
  -f "title=chore: update Distroplane formula to $tag" \
  -f 'body=Updates the CLI and bundled provider formula from verified published release archives. Review the Homebrew audit, installation, and lifecycle checks before merging.' \
  -f "head=$branch" -f 'base=main' >/dev/null
printf 'Opened Homebrew tap update PR for %s; merge requires maintainer approval.\n' "$tag"
)
    matches = re.findall(pattern, source, re.M)
    targets = {(system, arch) for _, system, arch in matches}
    if (len(matches) != 4 or len(re.findall(r'^\s*url ', source, re.M)) != 4
            or targets != {('darwin', 'arm64'), ('darwin', 'amd64'),
                           ('linux', 'arm64'), ('linux', 'amd64')}):
        raise SystemExit('tap formula has unrecognized release URLs; review required')
    versions = {version for version, _, _ in matches}
    if len(versions) != 1:
        raise SystemExit('tap formula has inconsistent archive versions; review required')
    return tuple(int(n) for n in versions.pop().split('.'))
old_v, new_v = formula_version(old), formula_version(new)
if old_v > new_v or (old_v == new_v and old != new):
    raise SystemExit('tap formula differs at same/newer version; refusing downgrade or overwrite')
PY
  if cmp -s "$work/current.rb" "$work/distroplane.rb"; then
    echo "tap formula for $tag already published; no change"
    exit 0
  fi
fi

branch_record="$(read_optional "repos/$tap_repo/git/ref/heads/$branch")"
write_formula() {
  local content
  content="$(base64 < "$work/distroplane.rb" | tr -d '\n')"
  api --method PUT "repos/$tap_repo/contents/$formula_path" \
    -f "message=chore: update Distroplane Homebrew formula to $tag" \
    -f "content=$content" -f "branch=$branch" "$@" >/dev/null
}
if [[ -n "$branch_record" ]]; then
  existing_record="$(read_optional "repos/$tap_repo/contents/$formula_path?ref=$branch")"
  if [[ -z "$existing_record" ]]; then
    branch_sha="$(jq -er '.object.sha' <<<"$branch_record")"
    if [[ "$branch_sha" != "$main_sha" ]]; then
      echo 'existing tap branch differs from main and lacks a formula; review required' >&2
      exit 1
    fi
    write_formula
  else
    jq -er '.content' <<<"$existing_record" | base64 --decode > "$work/branch.rb"
    if ! cmp -s "$work/branch.rb" "$work/distroplane.rb"; then
      echo 'tap update branch has conflicting upstream changes; review required' >&2
      exit 1
    fi
  fi
else
  actual_sha="$(api "repos/$tap_repo/git/ref/heads/main" --jq '.object.sha')"
  if [[ "$actual_sha" != "$main_sha" ]]; then
    echo 'tap main changed while preparing update; rerun after review' >&2
    exit 1
  fi
  api --method POST "repos/$tap_repo/git/refs" -f "ref=refs/heads/$branch" -f "sha=$main_sha" >/dev/null
  sha_arg=()
  if [[ -n "$main_record" ]]; then
    sha_arg+=( -f "sha=$(jq -er '.sha' <<<"$main_record")" )
  fi
  write_formula "${sha_arg[@]}"
fi

prs="$(api "repos/$tap_repo/pulls?state=all&head=aalsanie:$branch&per_page=50")"
if jq -e 'any(.[]; .state == "closed")' <<<"$prs" >/dev/null; then
  echo 'tap update PR was previously closed; human review required' >&2
  exit 1
fi
if jq -e 'any(.[]; .state == "open")' <<<"$prs" >/dev/null; then
  echo "existing reviewed tap update PR for $tag; no duplicate created"
  exit 0
fi
api --method POST "repos/$tap_repo/pulls" \
  -f "title=chore: update Distroplane formula to $tag" \
  -f 'body=Updates the CLI and bundled provider formula from verified published release archives. Review the Homebrew audit, installation, and lifecycle checks before merging.' \
  -f "head=$branch" -f 'base=main' >/dev/null
printf 'Opened Homebrew tap update PR for %s; merge requires maintainer approval.\n' "$tag"
