#!/bin/bash
# Double-click to install leantoken into /usr/local/bin (asks for your password once).
set -e
here="$(cd "$(dirname "$0")" && pwd)"
dest=/usr/local/bin
sudo mkdir -p "$dest"
sudo cp "$here/leantoken" "$dest/leantoken"
sudo chmod +x "$dest/leantoken"
# the binary is unsigned; clear the download quarantine so Gatekeeper lets it run
sudo xattr -dr com.apple.quarantine "$dest/leantoken" 2>/dev/null || true
echo
echo "Installed $dest/leantoken"
echo "Start Bifrost (npx -y @maximhq/bifrost), then run: leantoken"
