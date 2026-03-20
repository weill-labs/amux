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

refuse_cross_checkout_overwrite() {
	echo "amux build: refusing to overwrite $dest" >&2
	echo "  installed from: $current_source" >&2
	echo "  current repo:    $repo_root" >&2
	echo "Use AMUX_INSTALL_FORCE=1 make build to replace the shared binary intentionally." >&2
	exit 1
}

if [[ "${AMUX_INSTALL_FORCE:-0}" != "1" && -f "$meta_path" ]]; then
	current_source="$(awk -F= '$1=="source_repo"{print substr($0, index($0, "=") + 1)}' "$meta_path")"
	if [[ -n "$current_source" && "$current_source" != "$repo_root" ]]; then
		refuse_cross_checkout_overwrite
	fi
fi

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

cat >"$meta_tmp" <<EOF
source_repo=$repo_root
repo_name=$repo_name
branch=$branch
revision=$revision
built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

mv "$tmp" "$dest"
mv "$meta_tmp" "$meta_path"
