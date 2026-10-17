#!/bin/sh
# Installs warrant, warrantd and banksim from the latest GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/Rakshit-gen/warrant/main/scripts/install.sh | sh
#
# Checks the archive against the release's checksums and puts the binaries
# in ~/.local/bin. Nothing else is needed: no Go, no Docker.
#
#   WARRANT_VERSION   version to install (default: latest)
#   WARRANT_BIN_DIR   where to put the binaries (default: $HOME/.local/bin)
set -eu

REPO="Rakshit-gen/warrant"
VERSION="${WARRANT_VERSION:-latest}"
BIN_DIR="${WARRANT_BIN_DIR:-$HOME/.local/bin}"

say() { printf '  %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

if command -v curl >/dev/null 2>&1; then
	get() { curl -fsSL "$1" -o "$2"; }
	show() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
	get() { wget -q "$1" -O "$2"; }
	show() { wget -qO- "$1"; }
else
	die "need curl or wget"
fi

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "warrant ships for macOS and Linux, not $(uname -s)" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "warrant ships for amd64 and arm64, not $(uname -m)" ;;
esac

if [ "$VERSION" = latest ]; then
	VERSION="$(show "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
	[ -n "$VERSION" ] || die "could not find the latest release; set WARRANT_VERSION"
fi
tag="v${VERSION#v}"
archive="warrant_${tag#v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/warrant.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

say "downloading $archive"
get "$base/$archive" "$tmp/$archive" || die "download failed: $base/$archive"
get "$base/checksums.txt" "$tmp/checksums.txt" || die "no checksums.txt in release $tag"
want="$(grep " $archive\$" "$tmp/checksums.txt" | awk '{print $1}')"
if command -v sha256sum >/dev/null 2>&1; then
	got="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
else
	got="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
fi
[ -n "$want" ] && [ "$want" = "$got" ] || die "checksum does not match for $archive"
say "checksum ok"

tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$BIN_DIR"
for b in warrant warrantd banksim; do
	[ -f "$tmp/$b" ] || die "$b is missing from the archive"
	mv "$tmp/$b" "$BIN_DIR/$b"
	chmod +x "$BIN_DIR/$b"
done
say "installed warrant, warrantd and banksim $tag to $BIN_DIR"

case ":$PATH:" in
*":$BIN_DIR:"*) say "try: warrant help" ;;
*) say "$BIN_DIR is not on your PATH; add it, then try: warrant help" ;;
esac
