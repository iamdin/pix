#!/bin/sh
# Install pitch: curl -fsSL https://raw.githubusercontent.com/iamdin/pitch/main/install.sh | sh
set -eu

dir=${PITCH_INSTALL_DIR:-$HOME/.local/bin}
base=${PITCH_DOWNLOAD:-https://github.com/iamdin/pitch/releases/latest/download}
asset=pitch-darwin-arm64

[ "$(uname -s)/$(uname -m)" = Darwin/arm64 ] || { echo "pitch: only macOS on Apple silicon is supported" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$base/$asset" -o "$tmp/pitch"
curl -fsSL "$base/$asset.sha256" -o "$tmp/pitch.sha256"
[ "$(shasum -a 256 "$tmp/pitch" | cut -d' ' -f1)" = "$(cut -d' ' -f1 "$tmp/pitch.sha256")" ] || { echo "pitch: checksum mismatch" >&2; exit 1; }

mkdir -p "$dir"
install -m 755 "$tmp/pitch" "$dir/pitch"
echo "Installed pitch $("$dir/pitch" --version) to $dir/pitch"

case ":$PATH:" in
*":$dir:"*) ;;
*) echo "Add $dir to PATH: echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc" ;;
esac
for tool in git node npm; do
	command -v "$tool" >/dev/null || echo "Note: pitch build needs $tool (Node 24+)"
done
