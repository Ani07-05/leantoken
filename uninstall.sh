#!/bin/sh
# Uninstall slendertoken on macOS or Linux:
#   curl -fsSL https://raw.githubusercontent.com/Ani07-05/slendertoken/main/uninstall.sh | sh
# Pass --yes to skip the confirmation prompt. SLENDERTOKEN_BIN_DIR selects a custom binary directory.
set -eu

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
  echo "Usage: uninstall.sh [--yes]"
  echo "Removes the slendertoken executable and ~/.slendertoken (settings, provider keys, Bifrost gateway and data)."
  echo "Set SLENDERTOKEN_BIN_DIR when the executable was installed in a custom directory."
  exit 0
fi
if [ "${1:-}" != "" ] && [ "${1:-}" != "--yes" ]; then
  echo "unknown option: $1 (try --help)" >&2
  exit 2
fi

home=${HOME:?HOME is not set}
app_dir="$home/.slendertoken"

if [ "${1:-}" != "--yes" ]; then
  if [ ! -r /dev/tty ]; then
    echo "confirmation needs a terminal; run with --yes to proceed" >&2
    exit 1
  fi
  echo "This removes the slendertoken executable and $app_dir, including saved provider keys and Bifrost data."
  printf "Continue? [y/N] " > /dev/tty
  IFS= read -r answer < /dev/tty || answer=
  case "$answer" in y|Y|yes|YES) ;; *) echo "cancelled"; exit 0 ;; esac
fi

# Stop the gateway only when its recorded PID still identifies the installed Bifrost executable.
pid_file="$app_dir/bifrost/bifrost.pid"
gateway="$app_dir/bifrost/bin/bifrost-http"
if [ -f "$pid_file" ] && [ -x "$gateway" ]; then
  pid=$(cat "$pid_file" 2>/dev/null || true)
  case "$pid" in
    ''|*[!0-9]*) ;;
    *)
      if command -v pgrep >/dev/null 2>&1 && pgrep -f "^$gateway([[:space:]]|$)" >/dev/null 2>&1; then
        kill "$pid" 2>/dev/null || true
      fi
      ;;
  esac
fi

remove_binary() {
  dir=$1
  if [ -f "$dir/slendertoken" ]; then
    if [ -w "$dir/slendertoken" ]; then rm -f "$dir/slendertoken"; else sudo rm -f "$dir/slendertoken"; fi
    echo "removed $dir/slendertoken"
  fi
}

if [ -n "${SLENDERTOKEN_BIN_DIR:-}" ]; then remove_binary "$SLENDERTOKEN_BIN_DIR"; fi
remove_binary "$home/.local/bin"
remove_binary /usr/local/bin

if [ -e "$app_dir" ]; then
  rm -rf "$app_dir"
  echo "removed $app_dir"
else
  echo "no $app_dir found"
fi

echo "Uninstalled. Per-repository .slendertoken run reports and .slendertoken-private markers were left untouched."
