#!/bin/bash
#
# Installs the project scaffolder so a new project can be created from anywhere:
#
#   go-boilerplate
#
# Re-run it after pulling changes to the boilerplate.

set -euo pipefail

source_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
target="/usr/local/bin/go-boilerplate"

if [ ! -w "$(dirname "$target")" ]; then
    echo "need write access to $(dirname "$target"), re-run with sudo" >&2
    exit 1
fi

install -m 0755 "$source_dir/go-boilerplate" "$target"

echo "installed $target"