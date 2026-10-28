#!/usr/bin/env bash
set -euo pipefail

# Exercise the real archive on the native Linux runner, without using Go or
# Homebrew in the installation path.
version="$VERSION"
test -n "$version"
dist="$(cd "$DISTROPLANE_RELEASE_SMOKE_DIR" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
prefix="$work/prefix"
mkdir -p "$work/project/.distroplane"
printf 'active run remains intact\n' > "$work/project/.distroplane/run.journal"

python3 scripts/install-linux.py --version "$version" --offline-dir "$dist" --prefix "$prefix"
test -L "$prefix/bin/distroplane"
"$prefix/bin/distroplane" version --json | python3 -c 'import json,os,sys; assert json.load(sys.stdin)["version"] == os.environ["VERSION"]'
for provider in homebrew npm sdkman winget; do
  test -x "$prefix/lib/distroplane/versions/$version/libexec/distroplane-provider-$provider"
done
# Repeat installation is an integrity-checked no-op.
python3 scripts/install-linux.py --version "$version" --offline-dir "$dist" --prefix "$prefix"

case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64) arch=arm64 ;;
  *) echo "unsupported native Linux architecture" >&2; exit 1 ;;
esac
printf -v asset 'distroplane_%s_linux_%s.tar.gz' "$version" "$arch"
mkdir "$work/tampered"
cp "$dist/RELEASE-METADATA.json" "$dist/SHA256SUMS" "$dist/$asset" "$work/tampered/"
printf 'corruption' >> "$work/tampered/$asset"
if python3 scripts/install-linux.py --version "$version" --offline-dir "$work/tampered" --prefix "$prefix"; then
  echo 'tampered release archive was accepted' >&2
  exit 1
fi
"$prefix/bin/distroplane" version --json | python3 -c 'import json,os,sys; assert json.load(sys.stdin)["version"] == os.environ["VERSION"]'

python3 scripts/install-linux.py --prefix "$prefix" --uninstall
test ! -e "$prefix/bin/distroplane"
test -x "$prefix/lib/distroplane/versions/$version/bin/distroplane"
test "$(cat "$work/project/.distroplane/run.journal")" = 'active run remains intact'
echo "Linux native release installation and recovery smoke passed ($arch)"
