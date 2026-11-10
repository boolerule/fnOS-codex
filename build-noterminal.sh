#!/usr/bin/env bash
# Build the installation console without PTY support for both fnOS architectures.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TERMINAL=0 exec bash "$root/build.sh" cross "${VERSION:-1.3.0}" "${1:-$root/dist}"
