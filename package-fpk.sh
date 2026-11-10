#!/bin/sh
# Package the two Linux executables produced by build.sh cross.
set -eu
root=$(CDPATH='' cd -P "$(dirname "$0")" && pwd)
cd "$root"
fnpack=${FNPACK_BIN:-fnpack}
fnpack=$(command -v "$fnpack") || { echo 'Install fnpack or set FNPACK_BIN to its executable path.' >&2; exit 1; }
case "$fnpack" in /*) ;; *) fnpack="$root/$fnpack";; esac
case "$(uname -s)" in
  Linux|Darwin) ;;
  *) echo 'Run packaging on a POSIX filesystem under Linux, WSL or macOS to preserve executable permissions.' >&2; exit 1;;
esac
version=${VERSION:-$(awk -F= '$1 ~ /^version[[:space:]]*$/ {gsub(/[[:space:]]/,"",$2);print $2}' fnpack/manifest)}
case "$version" in ''|*[!0-9a-zA-Z.+-]*) echo 'Invalid package version' >&2; exit 1;; esac

# Staging on a native filesystem also avoids fnpack modifying source manifests.
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/package" "$stage/output" dist
cp -R fnpack/. "$stage/package/"
pkg="$stage/package"
mkdir -p "$pkg/app/bin" "$pkg/app/ui/images" "$pkg/wizard"
cp "$pkg/ICON.PNG" "$pkg/app/ui/images/icon_64.png"
cp "$pkg/ICON_256.PNG" "$pkg/app/ui/images/icon_256.png"
cp LICENSE "$pkg/app/LICENSE"
find "$pkg" -type d -exec chmod 755 {} +
find "$pkg" -type f -exec chmod 644 {} +
chmod 755 "$pkg"/cmd/*
for arch in amd64 arm64; do
  case "$arch" in
    amd64) platform=x86; machine=3e00 ;;
    arm64) platform=arm; machine=b700 ;;
  esac
  binary="dist/codex-console-$arch"
  if [ ! -f "$binary" ]; then echo "Missing $binary. Run bash build.sh cross first." >&2; exit 1; fi
  magic=$(od -An -tx1 -N6 "$binary" | tr -d '[:space:]')
  actual=$(od -An -tx1 -j18 -N2 "$binary" | tr -d '[:space:]')
  if [ "$magic" != 7f454c460201 ] || [ "$actual" != "$machine" ]; then
    echo "Expected a Linux $arch ELF64 executable at $binary" >&2; exit 1
  fi
  cp "$binary" "$pkg/app/bin/codex-console"
  chmod 755 "$pkg/app/bin/codex-console"
  sed -e "s/^platform[[:space:]]*=.*/platform=$platform/" \
      -e "s/^version[[:space:]]*=.*/version=$version/" fnpack/manifest > "$pkg/manifest"
  (cd "$stage/output" && "$fnpack" build --directory "$pkg")
  mv "$stage/output/CodexTerminal.fpk" "$stage/output/CodexTerminal-$version-$arch.fpk"
done
for fpk in "$stage"/output/*.fpk; do
  cp "$fpk" dist/
  echo "dist/$(basename "$fpk")"
done
