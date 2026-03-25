#!/usr/bin/env bash

set -euo pipefail

dest="${1:-$HOME/.local/bin/amux}"
dest_dir="$(dirname "$dest")"
meta_path="${dest}.install-meta"
repo_root="$(git rev-parse --show-toplevel)"
repo_name="${repo_root##*/}"
branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo detached)"
revision="$(git rev-parse HEAD 2>/dev/null || echo unknown)"

mkdir -p "$dest_dir"

tmp="$(mktemp "$dest_dir/.amux.tmp.XXXXXX")"
meta_tmp="$(mktemp "$dest_dir/.amux-meta.tmp.XXXXXX")"

cleanup() {
	rm -f "$tmp" "$meta_tmp"
}
trap cleanup EXIT

go build -o "$tmp" .

if [[ $(uname -s) == Darwin ]]; then
	# Strip com.apple.provenance xattr that macOS Sequoia sets on files
	# created in SSH sessions. Without this, taskgated rejects ad-hoc
	# signed binaries at launch (SIGKILL "Code Signature Invalid").
	xattr -d com.apple.provenance "$tmp" 2>/dev/null || true
	if ! codesign -f -s - "$tmp" >/dev/null 2>&1; then
		echo "amux build: codesign failed for $tmp" >&2
		exit 1
	fi
fi

if ! "$tmp" install-terminfo; then
	echo "amux build: install-terminfo failed" >&2
	exit 1
fi

cat >"$meta_tmp" <<EOF
source_repo=$repo_root
repo_name=$repo_name
branch=$branch
revision=$revision
built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

mv "$tmp" "$dest"
mv "$meta_tmp" "$meta_path"
