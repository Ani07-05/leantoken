#!/bin/sh
# Install slendertoken on macOS or Linux:
#   curl -fsSL https://raw.githubusercontent.com/Ani07-05/slendertoken/main/install.sh | sh
# Downloads the single static binary from the latest GitHub release. No Python, Node or Go needed.
# SLENDERTOKEN_VERSION=v0.2.0 pins a release; SLENDERTOKEN_BIN_DIR overrides the install directory.
set -eu

REPO="Ani07-05/slendertoken"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  darwin|linux) ;;
  *) echo "unsupported OS: $os (Windows: irm https://raw.githubusercontent.com/$REPO/main/install.ps1 | iex)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported CPU: $(uname -m)" >&2; exit 1 ;;
esac

if [ -n "${SLENDERTOKEN_VERSION:-}" ]; then
  base="https://github.com/$REPO/releases/download/$SLENDERTOKEN_VERSION"
else
  base="https://github.com/$REPO/releases/latest/download"
fi

dir="${SLENDERTOKEN_BIN_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

file="slendertoken_${os}_${arch}.tar.gz"
echo "downloading $file..."
curl -fsSL "$base/$file" -o "$tmp/$file"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

want="$(grep " $file\$" "$tmp/checksums.txt" | awk '{print $1}')"
if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$tmp/$file" | awk '{print $1}')"; else got="$(shasum -a 256 "$tmp/$file" | awk '{print $1}')"; fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "checksum mismatch for $file, refusing to install" >&2
  exit 1
fi

tar -xzf "$tmp/$file" -C "$tmp"
install -m 0755 "$tmp/slendertoken" "$dir/slendertoken"
# the binary is unsigned; clear the download quarantine so Gatekeeper lets it run
[ "$os" = darwin ] && xattr -d com.apple.quarantine "$dir/slendertoken" 2>/dev/null || true

echo
echo "installed $dir/slendertoken"
case ":$PATH:" in *":$dir:"*) ;; *) echo "add it to your PATH:  export PATH=\"$dir:\$PATH\"" ;; esac
echo "needs the Bifrost gateway:  npx -y @maximhq/bifrost"
echo "then run:  slendertoken"
