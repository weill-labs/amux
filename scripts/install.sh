#!/usr/bin/env bash

set -euo pipefail

dest="${1:-$HOME/.local/bin/amux}"
dest_dir="$(dirname "$dest")"
meta_path="${dest}.install-meta"
repo_root="$(git rev-parse --show-toplevel)"
repo_name="${repo_root##*/}"
branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo detached)"
revision="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
socket_dir="/tmp/amux-$(id -u)"

extract_checkpoint_version_from_text() {
	local text="$1"
	sed -n 's/.*checkpoint v\([0-9][0-9]*\).*/\1/p' <<<"$text" | head -n1
}

extract_checkpoint_version_from_json() {
	local text="$1"
	sed -n 's/.*"checkpoint_version"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' <<<"$text" | head -n1
}

query_binary_checkpoint_version() {
	local bin="$1"
	local out version

	if [[ ! -x "$bin" ]]; then
		return 1
	fi

	if out="$("$bin" version --json 2>/dev/null)"; then
		version="$(extract_checkpoint_version_from_json "$out")"
		if [[ -n "$version" ]]; then
			printf '%s\n' "$version"
			return 0
		fi
	fi

	if out="$("$bin" version 2>/dev/null)"; then
		version="$(extract_checkpoint_version_from_text "$out")"
		if [[ -n "$version" ]]; then
			printf '%s\n' "$version"
			return 0
		fi
	fi

	return 1
}

read_meta_field() {
	local key="$1"

	if [[ ! -f "$meta_path" ]]; then
		return 1
	fi

	grep -E "^${key}=" "$meta_path" | head -n1 | cut -d= -f2-
}

installed_checkpoint_version_from_meta() {
	local version repo revision src

	version="$(read_meta_field checkpoint_version || true)"
	if [[ -n "$version" ]]; then
		printf '%s\n' "$version"
		return 0
	fi

	repo="$(read_meta_field source_repo || true)"
	revision="$(read_meta_field revision || true)"
	if [[ -z "$repo" || -z "$revision" ]]; then
		return 1
	fi

	src="$(git -C "$repo" show "${revision}:internal/checkpoint/checkpoint.go" 2>/dev/null || true)"
	version="$(sed -n 's/^const ServerCheckpointVersion = \([0-9][0-9]*\)$/\1/p' <<<"$src" | head -n1)"
	if [[ -z "$version" ]]; then
		return 1
	fi

	printf '%s\n' "$version"
}

discover_live_sessions() {
	local session sock out

	if [[ ! -x "$dest" || ! -d "$socket_dir" ]]; then
		return 0
	fi

	while IFS= read -r sock; do
		session="${sock##*/}"
		if out="$("$dest" -s "$session" status 2>/dev/null)"; then
			printf '%s\t%s\n' "$session" "$out"
		fi
	done < <(find "$socket_dir" -maxdepth 1 -type s -print 2>/dev/null | sort)
}

confirm_incompatible_install() {
	local running_version="$1"
	local new_version="$2"
	local session_count="$3"

	if [[ "${AMUX_INSTALL_ALLOW_INCOMPATIBLE_CHECKPOINT:-}" == "1" ]]; then
		return 0
	fi

	echo "warning: running amux server checkpoint version v${running_version} is incompatible with new binary checkpoint version v${new_version}." >&2
	echo "warning: replacing ${dest} will force ${session_count} live session(s) through the incompatible reload path and crash-checkpoint fallback." >&2
	printf 'Proceed with install? [y/N] ' >&2

	if [[ -t 0 ]]; then
		local reply
		read -r reply
		case "$reply" in
			y|Y|yes|YES|Yes)
				return 0
				;;
		esac
		echo "amux install aborted." >&2
		exit 1
	fi

	echo >&2
	echo "amux install aborted: set AMUX_INSTALL_ALLOW_INCOMPATIBLE_CHECKPOINT=1 to override in non-interactive mode." >&2
	exit 1
}

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

new_checkpoint_version="$(query_binary_checkpoint_version "$tmp" || true)"
if [[ -z "$new_checkpoint_version" ]]; then
	echo "amux build: unable to determine checkpoint version for $tmp" >&2
	exit 1
fi

live_session_count=0
running_checkpoint_version=""
while IFS=$'\t' read -r session status_out; do
	if [[ -z "$session" ]]; then
		continue
	fi
	live_session_count=$((live_session_count + 1))
	if [[ -z "$running_checkpoint_version" ]]; then
		running_checkpoint_version="$(extract_checkpoint_version_from_text "$status_out")"
	fi
done < <(discover_live_sessions)

if [[ -z "$running_checkpoint_version" ]]; then
	running_checkpoint_version="$(query_binary_checkpoint_version "$dest" || true)"
fi
if [[ -z "$running_checkpoint_version" ]]; then
	running_checkpoint_version="$(installed_checkpoint_version_from_meta || true)"
fi
if (( live_session_count > 0 )) && [[ -n "$running_checkpoint_version" && "$running_checkpoint_version" != "$new_checkpoint_version" ]]; then
	confirm_incompatible_install "$running_checkpoint_version" "$new_checkpoint_version" "$live_session_count"
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
checkpoint_version=$new_checkpoint_version
built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

mv "$tmp" "$dest"
mv "$meta_tmp" "$meta_path"
