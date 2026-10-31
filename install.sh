#!/bin/sh
# Install pitch: curl -fsSL https://raw.githubusercontent.com/iamdin/pitch/main/install.sh | sh
set -eu

dir=${PITCH_INSTALL_DIR:-$HOME/.local/bin}
base=${PITCH_DOWNLOAD:-https://github.com/iamdin/pitch/releases/latest/download}

case "$(uname -s)/$(uname -m)" in
Darwin/arm64) asset=pitch-darwin-arm64 ;;
Linux/x86_64) asset=pitch-linux-amd64 ;;
Linux/aarch64 | Linux/arm64) asset=pitch-linux-arm64 ;;
*) echo "pitch: supports macOS on Apple silicon and Linux on x86_64 or arm64" >&2; exit 1 ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading pitch (about 110 MB; Bun and the patched Pi are inside)..."
curl -fL --progress-bar "$base/$asset" -o "$tmp/pitch"
curl -fsSL "$base/$asset.sha256" -o "$tmp/pitch.sha256"
sum=$( (sha256sum "$tmp/pitch" 2>/dev/null || shasum -a 256 "$tmp/pitch") | cut -d' ' -f1)
[ "$sum" = "$(cut -d' ' -f1 "$tmp/pitch.sha256")" ] || { echo "pitch: checksum mismatch" >&2; exit 1; }

mkdir -p "$dir"
install -m 755 "$tmp/pitch" "$dir/pitch"
echo "Installed pitch $("$dir/pitch" --version) to $dir/pitch"

case ":$PATH:" in
*":$dir:"*) ;;
*) echo "Add $dir to PATH: export PATH=\"$dir:\$PATH\" in your shell's startup file" ;;
esac
echo "Next: pitch build (builds your Pi with its packages, about a minute), then pitch pi"
