import os
import re
import shutil
import subprocess
from pathlib import Path

MAX_OUT = 1600
MAX_LINES = 150  # per read_file call; every later turn pays for what is read now
SHELL = "Windows cmd.exe" if os.name == "nt" else "POSIX sh"
SAFE_CMD = re.compile(r"^(git (status|diff|log|show|branch)|ls|pwd|cat|head|tail|wc)\b[^;&|><`$]*$")


def clip(s: str, n: int = MAX_OUT) -> str:
    if len(s) <= n:
        return s
    return s[: n // 2] + f"\n...[{len(s) - n} chars cut]...\n" + s[-n // 2 :]


class Workspace:
    def __init__(self, root: Path):
        self.root = root.resolve()
        self.naive: int | None = None  # size of this call's output for an agent with no limits

    def _path(self, p: str) -> Path:
        full = (self.root / p).resolve()
        if self.root != full and self.root not in full.parents:
            raise ValueError(f"path escapes repo: {p}")
        return full

    def read_file(self, path: str, start: int = 1, end: int = 100, **alias) -> str:
        # models guess names like line_start/end_line; a wrong guess would cost a round trip
        for k, v in alias.items():
            if "start" in k:
                start = v
            elif "end" in k:
                end = v
            else:
                raise TypeError(f"unexpected argument {k}; use path, start, end")
        text = self._path(path).read_text(errors="replace")
        self.naive = len(text)
        lines = text.splitlines()
        start = max(int(start), 1)
        chunk = lines[start - 1 : min(int(end), start - 1 + MAX_LINES)]
        body = "\n".join(f"{i}\t{l}" for i, l in enumerate(chunk, start))
        return clip(body + f"\n[{len(lines)} lines total]")

    def search(self, pattern: str, path: str = ".") -> str:
        target = str(self._path(path))
        if shutil.which("rg"):
            cmd = ["rg", "-n", "--no-heading", "-m", "5", pattern, target]
        else:
            cmd = ["grep", "-rnI", "-m", "5", "--exclude-dir=.git", "--exclude-dir=.leantoken", pattern, target]
        out = subprocess.run(cmd, capture_output=True, text=True, errors="replace", cwd=self.root).stdout
        self.naive = len(out)
        return clip(out.replace(str(self.root) + "/", "") or "no matches")

    def write_file(self, path: str, content: str) -> str:
        p = self._path(path)
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content)
        return f"wrote {path} ({len(content)} bytes)"

    def edit_file(self, path: str, old: str, new: str) -> str:
        p = self._path(path)
        text = p.read_text()
        if text.count(old) == 0 and "	" in old:  # models often re-indent with tabs
            old, new = old.expandtabs(4), new.expandtabs(4)
        if text.count(old) != 1:
            return f"error: old string matched {text.count(old)} times, need exactly 1"
        p.write_text(text.replace(old, new))
        return f"edited {path}"

    def web_search(self, query: str, max_results: int = 3) -> str:
        from .web import search

        return search(query, max_results)

    def run_shell(self, cmd: str | list, timeout: int = 120) -> str:
        cmd = unwrap(cmd)
        # models sometimes send argv; run it as argv rather than re-quoting it
        r = subprocess.run(cmd, shell=isinstance(cmd, str), capture_output=True, text=True, errors="replace", cwd=self.root,
                           timeout=min(int(timeout), 120) if timeout else 120)
        raw = f"exit={r.returncode}\n{r.stdout}{r.stderr}"
        self.naive = len(raw)
        return clip(raw)


def unwrap(cmd: str | list) -> str | list:
    """["bash", "-lc", "pytest"] -> "pytest": models assume Linux, but on Windows bash is WSL without the repo's tools."""
    if isinstance(cmd, list) and len(cmd) == 3 and Path(cmd[0]).name in ("bash", "sh", "zsh") and cmd[1] in ("-c", "-lc"):
        return cmd[2]
    # a whole command line inside one argv element, e.g. ["pytest -q"], is not an executable name
    if isinstance(cmd, list) and cmd and " " in cmd[0]:
        return " ".join(cmd)
    return cmd


def shell_cmd(args: dict) -> str:
    cmd = unwrap(args.get("cmd", ""))
    return " ".join(cmd) if isinstance(cmd, list) else str(cmd)


def needs_approval(name: str, args: dict) -> bool:
    if name in ("read_file", "search", "web_search"):
        return False
    if name == "run_shell":
        return not SAFE_CMD.match(shell_cmd(args))
    return True


def describe(name: str, args: dict) -> str:
    if name == "edit_file":
        return f"edit {args['path']}\n- {args['old']}\n+ {args['new']}"
    if name == "write_file":
        return f"write {args['path']} ({len(args['content'])} bytes)"
    return f"shell: {shell_cmd(args)}"


def _fn(name, desc, props, required):
    return {
        "type": "function",
        "function": {
            "name": name,
            "description": desc,
            "parameters": {"type": "object", "properties": props, "required": required},
        },
    }


S = {"type": "string"}
I = {"type": "integer"}
CMD = {"anyOf": [S, {"type": "array", "items": S}]}
SCHEMAS = [
    _fn("read_file", "Read lines start..end (max 150).", {"path": S, "start": I, "end": I}, ["path"]),
    _fn("search", "Regex search, 5 hits per file.", {"pattern": S, "path": S}, ["pattern"]),
    _fn("edit_file", "Replace one unique exact string.", {"path": S, "old": S, "new": S}, ["path", "old", "new"]),
    _fn("write_file", "Create or overwrite a file.", {"path": S, "content": S}, ["path", "content"]),
    _fn("run_shell", f"Run a {SHELL} command (tests, git). Never ls -R or cat big files.", {"cmd": CMD}, ["cmd"]),
]

WEB_SCHEMA = _fn("web_search", "Web search for current docs. Only if the repo cannot answer.", {"query": S, "max_results": I}, ["query"])


def schemas(web: bool = False) -> list:
    return SCHEMAS + [WEB_SCHEMA] if web else SCHEMAS
