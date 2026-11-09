#!/bin/bash
# Wrap dist/leantoken in a .dmg with a double-click installer. Usage: packaging/make_dmg.sh <arch>
set -euo pipefail
arch="$1"
stage="$(mktemp -d)/leantoken"
mkdir -p "$stage"
cp dist/leantoken packaging/install.command "$stage/"
chmod +x "$stage/leantoken" "$stage/install.command"
cat > "$stage/README.txt" <<TXT
leantoken for macOS ($arch)

1. Double-click install.command (if macOS blocks it: right-click > Open).
2. Start the gateway: npx -y @maximhq/bifrost
3. Run: leantoken    (first run opens Settings for your Bifrost virtual key)
TXT
hdiutil create -volname "leantoken" -srcfolder "$stage" -ov -format UDZO "dist/leantoken-macos-$arch.dmg"
