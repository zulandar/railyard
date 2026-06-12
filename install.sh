#!/bin/sh
# install.sh — Install the prebuilt `ry` binary from the latest GitHub Release.
#
# No Go toolchain required. Downloads the correct release tarball for your
# OS/arch, extracts the binary, and installs it as `ry`.
#
# Quick start:
#   curl -fsSL https://raw.githubusercontent.com/zulandar/railyard/main/install.sh | sh
#
# Environment overrides:
#   RAILYARD_VERSION      Install a specific tag (e.g. v0.9.15) instead of latest.
#   RAILYARD_INSTALL_DIR  Install location (default: ~/.local/bin).
#
# Prefer building from source? With a Go toolchain installed:
#   go install github.com/zulandar/railyard/cmd/ry@latest

set -eu

# --- Constants ---------------------------------------------------------------

REPO="zulandar/railyard"
RELEASES_PAGE="https://github.com/${REPO}/releases/latest"
LATEST_API="https://api.github.com/repos/${REPO}/releases/latest"
GO_INSTALL="go install github.com/zulandar/railyard/cmd/ry@latest"

# --- Output helpers ----------------------------------------------------------

info() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }

err() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

# --- Prerequisite checks -----------------------------------------------------

need() {
	command -v "$1" >/dev/null 2>&1 || err "required command '$1' not found in PATH. Please install it and re-run."
}

need curl
need tar
need uname

# --- OS / architecture detection ---------------------------------------------

detect_os() {
	uname_s="$(uname -s)"
	case "$uname_s" in
	Linux) printf 'linux' ;;
	Darwin) printf 'darwin' ;;
	MINGW* | MSYS* | CYGWIN* | Windows*)
		err "Windows is not supported by this installer. Please install and run Railyard inside WSL (Windows Subsystem for Linux), then re-run this script from your WSL shell."
		;;
	*)
		err "unsupported operating system: '${uname_s}'. Build from source with:
    ${GO_INSTALL}
  or download a binary from:
    ${RELEASES_PAGE}"
		;;
	esac
}

detect_arch() {
	uname_m="$(uname -m)"
	case "$uname_m" in
	x86_64 | amd64) printf 'amd64' ;;
	aarch64 | arm64) printf 'arm64' ;;
	*)
		err "unsupported architecture: '${uname_m}'. Build from source with:
    ${GO_INSTALL}
  or download a binary from:
    ${RELEASES_PAGE}"
		;;
	esac
}

# --- Version resolution ------------------------------------------------------

# resolve_version prints the release tag to install (including leading 'v').
# Honors RAILYARD_VERSION if set; otherwise queries the GitHub API and falls
# back to following the /releases/latest redirect.
resolve_version() {
	if [ -n "${RAILYARD_VERSION:-}" ]; then
		printf '%s' "${RAILYARD_VERSION}"
		return 0
	fi

	# Primary: GitHub API. Parse "tag_name": "vX.Y.Z" without requiring jq.
	tag="$(curl -fsSL "${LATEST_API}" 2>/dev/null |
		grep -m1 '"tag_name"' |
		sed -e 's/.*"tag_name"[[:space:]]*:[[:space:]]*"//' -e 's/".*//')"

	if [ -n "${tag}" ]; then
		printf '%s' "${tag}"
		return 0
	fi

	# Fallback: follow the redirect on the releases/latest page; the final URL
	# ends in /tag/<TAG>.
	final_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "${RELEASES_PAGE}" 2>/dev/null || true)"
	tag="$(printf '%s' "${final_url}" | sed -n 's#.*/tag/##p')"

	if [ -n "${tag}" ]; then
		printf '%s' "${tag}"
		return 0
	fi

	err "could not determine the latest release version. Set RAILYARD_VERSION explicitly (e.g. RAILYARD_VERSION=v0.9.15), or download a binary from:
    ${RELEASES_PAGE}"
}

# --- PATH note ---------------------------------------------------------------

# in_path reports whether $1 is an entry in $PATH.
in_path() {
	case ":${PATH}:" in
	*":$1:"*) return 0 ;;
	*) return 1 ;;
	esac
}

# --- Main --------------------------------------------------------------------

main() {
	OS="$(detect_os)"
	ARCH="$(detect_arch)"

	VERSION="$(resolve_version)"
	info "Installing ry ${VERSION} for ${OS}/${ARCH}..."

	asset="ry-${VERSION}-${OS}-${ARCH}"
	tarball="${asset}.tar.gz"
	url="https://github.com/${REPO}/releases/download/${VERSION}/${tarball}"

	# Install directory (default ~/.local/bin).
	INSTALL_DIR="${RAILYARD_INSTALL_DIR:-${HOME}/.local/bin}"

	# Temp workspace, cleaned up on exit.
	tmp="$(mktemp -d 2>/dev/null || mktemp -d -t ry-install)"
	# shellcheck disable=SC2064
	trap "rm -rf \"${tmp}\"" EXIT INT TERM

	info "Downloading ${url}"
	if ! curl -fsSL "${url}" -o "${tmp}/${tarball}"; then
		err "download failed: ${url}
  The release '${VERSION}' may not publish a ${OS}/${ARCH} build, or the network is unavailable.
  Available downloads: ${RELEASES_PAGE}
  Or build from source with: ${GO_INSTALL}"
	fi

	info "Extracting..."
	tar -xzf "${tmp}/${tarball}" -C "${tmp}"

	# The tarball contains a single binary named literally like the asset
	# (e.g. ry-v0.9.15-linux-amd64), NOT 'ry'. Install it renamed to 'ry'.
	if [ ! -f "${tmp}/${asset}" ]; then
		err "expected binary '${asset}' not found inside ${tarball}. The release layout may have changed; please report this at ${RELEASES_PAGE}"
	fi

	mkdir -p "${INSTALL_DIR}"
	install_path="${INSTALL_DIR}/ry"
	# Use cp + chmod (portable; `install` is not guaranteed on all systems).
	cp "${tmp}/${asset}" "${install_path}"
	chmod +x "${install_path}"

	info ""
	info "Installed ry to ${install_path}"

	# Report the installed version straight from the binary.
	if "${install_path}" version 2>/dev/null; then
		:
	else
		warn "installed binary did not run cleanly on this platform; got an error from '${install_path} version'."
	fi

	# PATH guidance.
	if ! in_path "${INSTALL_DIR}"; then
		info ""
		info "NOTE: ${INSTALL_DIR} is not on your PATH."
		info "Add it by appending this line to your shell rc (~/.bashrc, ~/.zshrc, ~/.profile):"
		info ""
		info "    export PATH=\"${INSTALL_DIR}:\$PATH\""
		info ""
		info "Then restart your shell or run: export PATH=\"${INSTALL_DIR}:\$PATH\""
	fi

	# Next steps.
	info ""
	info "Next steps:"
	info "    cd your-project && ry init"
}

main "$@"

# --- Optional: checksum verification (NICE-TO-HAVE, not currently published) -
# The release workflow does not publish a checksums file today, so the
# installer does not verify checksums. If a checksums.txt is added to releases
# in the future, fetch it alongside the tarball and verify before install, e.g.:
#
#   sums_url="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"
#   if curl -fsSL "${sums_url}" -o "${tmp}/checksums.txt" 2>/dev/null; then
#     ( cd "${tmp}" && grep " ${tarball}\$" checksums.txt | sha256sum -c - )
#   fi
