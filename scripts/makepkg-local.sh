#!/usr/bin/env bash
# makepkg-local.sh -- Build the Arch package from this checkout's HEAD.
#
# PKGBUILD's source is the GitHub repository's default branch, which is what
# AUR users build. CI and the release must package the commit under test
# instead, so this builds from a copy of PKGBUILD whose source is this
# checkout, fixed to HEAD's commit. The package files land in the checkout
# root (*.pkg.tar.zst).
#
# Usage: scripts/makepkg-local.sh   (as a non-root user; makepkg refuses root)

set -euo pipefail

repo=$(git rev-parse --show-toplevel)
commit=$(git -C "$repo" rev-parse HEAD)

# makepkg mirror-clones the source, which copies refs, not a detached HEAD
# (CI checks out a merge commit or a tag). A branch makes HEAD reachable.
git -C "$repo" branch -f makepkg-local "$commit" >/dev/null

build=$(mktemp -d)
trap 'rm -rf "$build"' EXIT

cp "$repo/PKGBUILD" "$repo/fpsd.install" "$build/"
sed -i "s|^source=(.*)$|source=(\"face-puncher-supreme::git+file://${repo}#commit=${commit}\")|" "$build/PKGBUILD"
grep -q "git+file://${repo}#commit=${commit}" "$build/PKGBUILD" \
    || { echo "makepkg-local: could not rewrite PKGBUILD source" >&2; exit 1; }

(cd "$build" && makepkg -s --noconfirm)
mv -v "$build"/*.pkg.tar.zst "$repo/"
