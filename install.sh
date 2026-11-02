#!/bin/sh
# Install or update pix: curl -fsSL https://github.com/iamdin/pix/releases/latest/download/install.sh | sh
# Each release carries this script; `pix update` runs the latest one. PIX_VERSION=X installs release X (0.0.4 or
# later) instead, to go back after a bad release.
set -eu

dir=${PIX_INSTALL_DIR:-$HOME/.local/bin}
if [ -n "${PIX_VERSION:-}" ]; then
	url=https://github.com/iamdin/pix/releases/download/v$PIX_VERSION
else
	url=https://github.com/iamdin/pix/releases/latest/download
fi
base=${PIX_DOWNLOAD:-$url}

case "$(uname -s)/$(uname -m)" in
Darwin/arm64) asset=pix-darwin-arm64 ;;
Linux/x86_64) asset=pix-linux-amd64 ;;
Linux/aarch64 | Linux/arm64) asset=pix-linux-arm64 ;;
*) echo "pix: supports macOS on Apple silicon and Linux on x86_64 or arm64" >&2; exit 1 ;;
esac

sha() { (sha256sum "$1" 2>/dev/null || shasum -a 256 "$1") | cut -d' ' -f1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$base/$asset.sha256" -o "$tmp/pix.sha256"
want=$(cut -d' ' -f1 "$tmp/pix.sha256")
if [ -x "$dir/pix" ] && [ "$(sha "$dir/pix")" = "$want" ]; then
	echo "pix $("$dir/pix" self version) in $dir is up to date"
else
	echo "Downloading pix (about 70 MB, with Bun for \`pix compile\`)..."
	curl -fL --progress-bar "$base/$asset" -o "$tmp/pix"
	[ "$(sha "$tmp/pix")" = "$want" ] || { echo "pix: checksum mismatch" >&2; exit 1; }
	mkdir -p "$dir"
	install -m 755 "$tmp/pix" "$dir/pix"
	echo "Installed pix $("$dir/pix" self version) to $dir/pix"
fi
# The Pi that the release compiled for this platform, about 55 MB; nothing is built here. After an update, the packages
# are compiled into the new Pi again if they were compiled into the old one.
"$dir/pix" self install

case ":$PATH:" in
*":$dir:"*) ;;
*) echo "Add $dir to PATH: export PATH=\"$dir:\$PATH\" in your shell's startup file" ;;
esac
echo "Next: run pix as you run pi"
