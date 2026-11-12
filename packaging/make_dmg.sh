#!/bin/bash
# Wrap dist/slendertoken in a .dmg with a double-click installer. Usage: packaging/make_dmg.sh <arch>
set -euo pipefail
arch="$1"
stage="$(mktemp -d)/slendertoken"
mkdir -p "$stage"
cp dist/slendertoken packaging/install.command "$stage/"
chmod +x "$stage/slendertoken" "$stage/install.command"
cat > "$stage/README.txt" <<TXT
slendertoken for macOS ($arch)

1. Double-click install.command (if macOS blocks it: right-click > Open).
2. Start the gateway: npx -y @maximhq/bifrost
3. Run: slendertoken    (first run opens Settings for your Bifrost virtual key)
TXT
hdiutil create -volname "slendertoken" -srcfolder "$stage" -ov -format UDZO "dist/slendertoken-macos-$arch.dmg"
