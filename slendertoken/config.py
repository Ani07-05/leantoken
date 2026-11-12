"""Settings: real env > ./.env > ~/.slendertoken/.env, so the exe works from any folder."""

import os
import subprocess
import sys
from pathlib import Path

# snapshot before any .env is loaded, so `config list` can tell sources apart
REAL_ENV = dict(os.environ)
GLOBAL_ENV = Path.home() / ".slendertoken" / ".env"
SECRET = ("KEY", "VK", "TOKEN", "PASSWORD")
TEMPLATE = """\
# slendertoken settings, used from any folder. A .env in the current folder overrides these.
# Edit with `slendertoken config edit` or `slendertoken config set KEY VALUE`.

# Bifrost gateway: every model call goes through it
BIFROST_URL=http://localhost:8080
# your virtual key (budget + rate limit), created in the Bifrost UI
BIFROST_VK=
# Ollama-only virtual key for --private runs
BIFROST_PRIVATE_VK=
# optional: skip the picker; otherwise run `slendertoken --pick-model`
# SLENDERTOKEN_MODEL=groq/openai/gpt-oss-120b
# optional: small model tried first (default groq/openai/gpt-oss-20b)
# SLENDERTOKEN_SMALL_MODEL=groq/openai/gpt-oss-20b
# optional: grid intensity in gCO2e/kWh instead of the Groq-region default
# SLENDERTOKEN_GRID_G=369
# optional: provider/model list Bifrost tries if Groq fails
# SLENDERTOKEN_FALLBACKS=openai/gpt-4o-mini
# optional: local model for --private (served by Ollama through Bifrost)
# SLENDERTOKEN_LOCAL_MODEL=qwen2.5-coder:7b
# optional: backup budget price in USD per 1M tokens "in,out"
# SLENDERTOKEN_PRICE=0.59,0.79
# optional: enables the web_search tool (app.tavily.com); never used in private mode
# TAVILY_API_KEY=tvly-
# optional: color theme: slender, ember, nord, gruvbox, dracula or paper; change with /theme
# SLENDERTOKEN_THEME=lean
"""
USAGE = """usage: slendertoken config [list | get KEY | set KEY VALUE | unset KEY | edit | path]
  list            show settings in effect and where each comes from (secrets masked)
  get KEY         print one value
  set KEY VALUE   save to the global file
  unset KEY       remove from the global file
  edit            open the global file in your editor
  path            print the global file's location"""


def parse(path: Path) -> dict[str, str]:
    if not path.is_file():
        return {}
    out = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip().strip("\"'")
    return out


def load_env(path: Path) -> None:
    """Minimal .env loader; values already set win."""
    for k, v in parse(path).items():
        if v:
            os.environ.setdefault(k, v)


def load_all() -> None:
    load_env(Path.cwd() / ".env")
    load_env(GLOBAL_ENV)


def ensure_global() -> Path:
    if not GLOBAL_ENV.exists():
        GLOBAL_ENV.parent.mkdir(parents=True, exist_ok=True)
        GLOBAL_ENV.write_text(TEMPLATE, encoding="utf-8")
    return GLOBAL_ENV


def set_value(key: str, value: str | None) -> None:
    """Set (or with None, remove) KEY in the global file, keeping comments and order."""
    path = ensure_global()
    lines, found = [], False
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.strip().split("=", 1)[0].strip() == key and "=" in line and not line.lstrip().startswith("#"):
            found = True
            if value is not None:
                lines.append(f"{key}={value}")
            continue
        lines.append(line)
    if value is not None and not found:
        lines.append(f"{key}={value}")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def mask(key: str, value: str) -> str:
    if value and is_secret(key):
        return value[:4] + "…" + value[-4:] if len(value) > 12 else "****"
    return value


def rows() -> list[tuple[str, str, str]]:
    """(key, masked value, source) for every known setting, in precedence order."""
    local, glob = parse(Path.cwd() / ".env"), parse(GLOBAL_ENV)
    out = []
    for k in dict.fromkeys([*parse_template_keys(), *glob, *local]):
        for src, vals in (("environment", REAL_ENV), ("./.env", local), ("global", glob)):
            if vals.get(k):
                out.append((k, mask(k, vals[k]), src))
                break
        else:
            out.append((k, "", "not set"))
    return out


def fields() -> list[tuple[str, str, str, str]]:
    """(key, description, default, saved global value) for the settings page, from TEMPLATE."""
    glob, out, desc = parse(GLOBAL_ENV), [], ""
    for line in TEMPLATE.splitlines():
        body = line.lstrip("# ")
        key, _, default = body.partition("=")
        if "=" in body and key.isupper():
            out.append((key, desc.removeprefix("optional: "), default, glob.get(key, "")))
        elif line.startswith("#"):
            desc = body
    return out


def is_secret(key: str) -> bool:
    return any(s in key.upper() for s in SECRET)


def overridden(key: str) -> str:
    """Where a value that beats the global file comes from, or ''."""
    if REAL_ENV.get(key):
        return "environment variable"
    return "./.env" if parse(Path.cwd() / ".env").get(key) else ""


def parse_template_keys() -> list[str]:
    return [line.lstrip("# ").split("=", 1)[0] for line in TEMPLATE.splitlines()
            if "=" in line and line.lstrip("# ").split("=", 1)[0].isupper()]


def edit() -> None:
    path = ensure_global()
    editor = os.environ.get("EDITOR") or ("notepad" if sys.platform == "win32" else "nano")
    subprocess.call([editor, str(path)])


def main(args: list[str]) -> None:
    cmd, rest = (args[0], args[1:]) if args else ("list", [])
    if cmd == "list":
        width = max(len(k) for k, _, _ in rows())
        for k, v, src in rows():
            print(f"{k:<{width}}  {v or '-':<24}  {src}")
        print(f"\nglobal file: {GLOBAL_ENV}")
    elif cmd == "get" and len(rest) == 1:
        print(os.environ.get(rest[0], ""))
    elif cmd == "set" and len(rest) >= 2:
        set_value(rest[0], " ".join(rest[1:]))
        print(f"saved {rest[0]} to {GLOBAL_ENV}")
    elif cmd == "unset" and len(rest) == 1:
        set_value(rest[0], None)
        print(f"removed {rest[0]} from {GLOBAL_ENV}")
    elif cmd == "edit":
        edit()
    elif cmd == "path":
        print(ensure_global())
    else:
        raise SystemExit(USAGE)
