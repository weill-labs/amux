#!/usr/bin/env bash

set -euo pipefail

dest="${1:-$HOME/.local/bin/amux}"
dest_dir="$(dirname "$dest")"

mkdir -p "$dest_dir"
tmp="$(mktemp "$dest_dir/.amux.tmp.XXXXXX")"

cleanup() {
	rm -f "$tmp"
}
trap cleanup EXIT

go build -o "$tmp" .
mv "$tmp" "$dest"
