#!/usr/bin/env bash
# Build the Vue application and embed it in the fnOS console executable.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export GOCACHE="${GOCACHE:-$root/.gocache}"
export GOPATH="${GOPATH:-$root/.gopath}"
export PATH="/var/apps/nodejs_v24/target/bin:$PATH"
export VITE_TERMINAL="${TERMINAL:-1}"
version="${2:-1.3.0}"
tags=()
if [[ "$VITE_TERMINAL" == 0 ]]; then tags=(-tags noterminal); fi
(cd "$root/frontend" && npm ci --no-audit --no-fund && npm run build)
if [[ "$VITE_TERMINAL" == 0 ]] && grep -rIl --include='*.js' --include='*.css' xterm "$root/frontend/dist"; then
  echo 'xterm is present in a terminal-disabled build' >&2
  exit 1
fi
embed="$root/backend/embed"
rm -rf -- "$embed"
mkdir -p "$embed"
trap 'rm -rf -- "$embed"' EXIT
cp -R "$root/frontend/dist/." "$embed/"
go clean -cache
build() {
  (cd "$root/backend" && CGO_ENABLED=0 go build -trimpath -buildvcs=false "${tags[@]}" \
    -ldflags "-s -w -X main.consoleVersion=$version" -o "$1" .)
}
if [[ "${1:-dev}" == cross ]]; then
  out="${3:-$root/dist}"
  mkdir -p "$out"
  out="$(cd "$out" && pwd)"
  for arch in amd64 arm64; do
    GOOS=linux GOARCH="$arch" build "$out/codex-console-$arch"
  done
else
  build "$root/backend/codex-console"
fi
