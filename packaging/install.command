#!/bin/bash
# Double-click to install slendertoken into /usr/local/bin (asks for your password once).
set -e
here="$(cd "$(dirname "$0")" && pwd)"
dest=/usr/local/bin
sudo mkdir -p "$dest"
sudo cp "$here/slendertoken" "$dest/slendertoken"
sudo chmod +x "$dest/slendertoken"
# the binary is unsigned; clear the download quarantine so Gatekeeper lets it run
sudo xattr -dr com.apple.quarantine "$dest/slendertoken" 2>/dev/null || true
echo
echo "Installed $dest/slendertoken"
echo "Start Bifrost (npx -y @maximhq/bifrost), then run: slendertoken"
