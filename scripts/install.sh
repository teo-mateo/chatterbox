#!/bin/sh
# Installs chatterbox for Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/teo-mateo/chatterbox/main/scripts/install.sh | sh
#
# CHATTERBOX_VERSION      the release to install: a tag such as v0.1.0, or
#                         "edge" for the newest build of main. Default: the
#                         latest release.
# CHATTERBOX_INSTALL_DIR  where the binary goes. Default: ~/.local/bin
set -eu

repo="https://github.com/teo-mateo/chatterbox/releases"
version="${CHATTERBOX_VERSION:-}"
if [ -z "$version" ]; then
	base="$repo/latest/download"
else
	base="$repo/download/$version"
fi
dir="${CHATTERBOX_INSTALL_DIR:-$HOME/.local/bin}"

if [ "$(uname -s)" != "Linux" ]; then
	echo "install.sh: only Linux builds are published; build from source with 'make build'" >&2
	exit 1
fi
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*)
	echo "install.sh: no build for $(uname -m); build from source with 'make build'" >&2
	exit 1
	;;
esac
name="chatterbox-linux-$arch"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
echo "downloading $name (${version:-latest release})"
curl -fsSL -o "$tmp/$name" "$base/$name" || {
	echo "install.sh: no such release: ${version:-latest}; see $repo" >&2
	exit 1
}
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"
(cd "$tmp" && grep " $name\$" SHA256SUMS | sha256sum -c - >/dev/null) || {
	echo "install.sh: the checksum of $name does not match; nothing installed" >&2
	exit 1
}

mkdir -p "$dir"
chmod 755 "$tmp/$name"
mv "$tmp/$name" "$dir/chatterbox"
echo "installed $("$dir/chatterbox" version) in $dir"
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "note: $dir is not on your PATH" ;;
esac
