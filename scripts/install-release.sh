#!/usr/bin/env bash

set -euo pipefail

repo_base_url="${AMUX_INSTALL_BASE_URL:-https://github.com/weill-labs/amux}"
install_bin_dir="${AMUX_INSTALL_BIN_DIR:-$HOME/.local/bin}"
version="${AMUX_INSTALL_VERSION:-}"
skip_terminfo="${AMUX_INSTALL_SKIP_TERMINFO:-0}"

usage() {
	cat <<'EOF'
Usage: install-release.sh [--version VERSION] [--bin-dir DIR] [--skip-terminfo]
EOF
}

need_cmd() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "amux install: required command not found: $1" >&2
		exit 1
	fi
}

normalize_version() {
	local raw="$1"
	printf '%s' "${raw#v}"
}

detect_platform() {
	case "$(uname -s)" in
	Darwin)
		goos="darwin"
		;;
	Linux)
		goos="linux"
		;;
	*)
		echo "amux install: unsupported OS $(uname -s)" >&2
		exit 1
		;;
	esac

	case "$(uname -m)" in
	x86_64 | amd64)
		goarch="amd64"
		;;
	arm64 | aarch64)
		goarch="arm64"
		;;
	*)
		echo "amux install: unsupported architecture $(uname -m)" >&2
		exit 1
		;;
	esac
}

resolve_version() {
	if [[ -n "$version" ]]; then
		version="$(normalize_version "$version")"
		return
	fi

	local latest_url
	latest_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "${repo_base_url%/}/releases/latest")"
	case "$latest_url" in
	*/releases/tag/*)
		version="$(normalize_version "${latest_url##*/}")"
		;;
	*)
		echo "amux install: could not resolve latest release from ${repo_base_url%/}/releases/latest" >&2
		exit 1
		;;
	esac
}

checksum_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
		return
	fi
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
		return
	fi

	echo "amux install: need sha256sum or shasum to verify downloads" >&2
	exit 1
}

maybe_install_terminfo() {
	local bin_path="$1"

	if [[ "$skip_terminfo" == "1" ]]; then
		return
	fi
	if ! command -v tic >/dev/null 2>&1; then
		echo "amux install: installed binary, but skipped terminfo because 'tic' is not on PATH" >&2
		echo "amux install: run '$bin_path install-terminfo' after installing ncurses/tic" >&2
		return
	fi
	if ! "$bin_path" install-terminfo >/dev/null 2>&1; then
		echo "amux install: installed binary, but terminfo installation failed" >&2
		echo "amux install: run '$bin_path install-terminfo' manually" >&2
	fi
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--version)
		if [[ $# -lt 2 ]]; then
			usage >&2
			exit 1
		fi
		version="$2"
		shift 2
		;;
	--bin-dir)
		if [[ $# -lt 2 ]]; then
			usage >&2
			exit 1
		fi
		install_bin_dir="$2"
		shift 2
		;;
	--skip-terminfo)
		skip_terminfo=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "amux install: unknown argument: $1" >&2
		usage >&2
		exit 1
		;;
	esac
done

repo_base_url="${repo_base_url%/}"
install_bin_dir="${install_bin_dir%/}"

need_cmd curl
need_cmd tar
detect_platform
resolve_version

archive_name="amux_${version}_${goos}_${goarch}.tar.gz"
release_base="${repo_base_url}/releases/download/v${version}"
archive_url="${release_base}/${archive_name}"
checksums_url="${release_base}/checksums.txt"

tmpdir="$(mktemp -d)"
tmp_dest=""
cleanup() {
	rm -rf "$tmpdir"
	if [[ -n "$tmp_dest" ]]; then
		rm -f "$tmp_dest"
	fi
}
trap cleanup EXIT

archive_path="${tmpdir}/${archive_name}"
checksums_path="${tmpdir}/checksums.txt"
downloaded_bin="${tmpdir}/amux"

curl -fsSL "$archive_url" -o "$archive_path"
curl -fsSL "$checksums_url" -o "$checksums_path"

expected_checksum="$(awk -v name="$archive_name" '$2 == name {print $1}' "$checksums_path")"
if [[ -z "$expected_checksum" ]]; then
	echo "amux install: checksum for ${archive_name} not found in checksums.txt" >&2
	exit 1
fi

actual_checksum="$(checksum_file "$archive_path")"
if [[ "$actual_checksum" != "$expected_checksum" ]]; then
	echo "amux install: checksum mismatch for ${archive_name}" >&2
	exit 1
fi

tar xzf "$archive_path" -C "$tmpdir" amux
chmod +x "$downloaded_bin"

mkdir -p "$install_bin_dir"
dest_path="${install_bin_dir}/amux"
tmp_dest="$(mktemp "${install_bin_dir}/.amux.tmp.XXXXXX")"
cp "$downloaded_bin" "$tmp_dest"
chmod +x "$tmp_dest"
mv "$tmp_dest" "$dest_path"
tmp_dest=""

maybe_install_terminfo "$dest_path"

printf 'amux %s installed to %s\n' "$version" "$dest_path"
