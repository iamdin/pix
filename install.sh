#!/bin/sh
# Install pix: curl -fsSL https://raw.githubusercontent.com/iamdin/pix/main/install.sh | sh
set -eu

dir=${PIX_INSTALL_DIR:-$HOME/.local/bin}
base=${PIX_DOWNLOAD:-https://github.com/iamdin/pix/releases/latest/download}

case "$(uname -s)/$(uname -m)" in
Darwin/arm64) asset=pix-darwin-arm64 ;;
Linux/x86_64) asset=pix-linux-amd64 ;;
Linux/aarch64 | Linux/arm64) asset=pix-linux-arm64 ;;
*) echo "pix: supports macOS on Apple silicon and Linux on x86_64 or arm64" >&2; exit 1 ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading pix (about 110 MB; Bun and Pi are inside)..."
curl -fL --progress-bar "$base/$asset" -o "$tmp/pix"
curl -fsSL "$base/$asset.sha256" -o "$tmp/pix.sha256"
sum=$( (sha256sum "$tmp/pix" 2>/dev/null || shasum -a 256 "$tmp/pix") | cut -d' ' -f1)
[ "$sum" = "$(cut -d' ' -f1 "$tmp/pix.sha256")" ] || { echo "pix: checksum mismatch" >&2; exit 1; }

mkdir -p "$dir"
install -m 755 "$tmp/pix" "$dir/pix"
echo "Installed pix $("$dir/pix" self version) to $dir/pix"

case ":$PATH:" in
*":$dir:"*) ;;
*) echo "Add $dir to PATH: export PATH=\"$dir:\$PATH\" in your shell's startup file" ;;
esac
echo "Next: run pix as you run pi (the first run builds it for this machine, about a minute)"
