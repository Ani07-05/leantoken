import os
import re
import shlex
import shutil
import subprocess
from pathlib import Path

MAX_OUT = 1600
MAX_LINES = 150  # per read_file call; every later turn pays for what is read now
SHELL = "Windows cmd.exe" if os.name == "nt" else "POSIX sh"
SAFE_CMD = re.compile(r"^(git (status|diff|log|show|branch|ls-files)|ls|dir|pwd|cat|type|head|tail|wc)\b[^;&|><`$]*$")
SKIP_DIRS = {".git", ".leantoken", "node_modules", ".venv", "venv", "__pycache__", "dist", "build", ".mypy_cache", ".pytest_cache"}
MAX_FILES = 200


def clip(s: str, n: int = MAX_OUT) -> str:
    if len(s) <= n:
        return s
    return s[: n // 2] + f"\n...[{len(s) - n} chars cut]...\n" + s[-n // 2 :]


class Workspace:
    def __init__(self, root: Path, lean: bool = True):
        self.root = root.resolve()
        self.lean = lean  # False gives the benchmark's naive agent: whole files, unclipped output
        self.naive: int | None = None  # size of this call's output for an agent with no limits

    def _path(self, p: str) -> Path:
        full = (self.root / p).resolve()
        if self.root != full and self.root not in full.parents:
            raise ValueError(f"path escapes repo: {p}")
        return full

    def _clip(self, s: str) -> str:
        return clip(s) if self.lean else s

    def read_file(self, path: str, start: int = 1, end: int | None = None, **alias) -> str:
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
        stop = int(end) if end is not None else (start + 99 if self.lean else len(lines))
        chunk = lines[start - 1 : min(stop, start - 1 + MAX_LINES) if self.lean else stop]
        body = "\n".join(f"{i}\t{l}" for i, l in enumerate(chunk, start))
        return self._clip(body + f"\n[{len(lines)} lines total]")

    def search(self, pattern: str, path: str = ".") -> str:
        target = str(self._path(path))
        if shutil.which("rg"):
            cmd = ["rg", "-n", "--no-heading", "-m", "5", pattern, target]
        elif shutil.which("grep") and os.name != "nt":
            cmd = ["grep", "-rnI", "-m", "5", "--exclude-dir=.git", "--exclude-dir=.leantoken", pattern, target]
        else:
            out = self._py_search(pattern, Path(target))
            self.naive = len(out)
            return self._clip(out or "no matches")
        out = subprocess.run(cmd, capture_output=True, text=True, errors="replace", cwd=self.root).stdout
        self.naive = len(out)
        return self._clip(out.replace(str(self.root) + os.sep, "").replace(str(self.root) + "/", "") or "no matches")

    def _files(self, base: Path):
        if base.is_file():
            yield base
            return
        for dirpath, dirs, files in os.walk(base):
            dirs[:] = sorted(d for d in dirs if d not in SKIP_DIRS)
            for f in sorted(files):
                yield Path(dirpath) / f

    def _py_search(self, pattern: str, base: Path) -> str:
        """Plain-Python grep for machines without rg or grep (stock Windows)."""
        try:
            rx = re.compile(pattern)
        except re.error:
            rx = re.compile(re.escape(pattern))
        hits = []
        for f in self._files(base):
            try:
                if f.stat().st_size > 1_000_000:
                    continue
                text = f.read_text(errors="strict")
            except (OSError, UnicodeDecodeError):  # binary or unreadable
                continue
            n = 0
            for i, line in enumerate(text.splitlines(), 1):
                if rx.search(line):
                    hits.append(f"{f.relative_to(self.root).as_posix()}:{i}:{line.strip()[:200]}")
                    n += 1
                    if n == 5:
                        break
        return "\n".join(hits)

    def list_files(self, path: str = ".") -> str:
        base = self._path(path)
        files = [f.relative_to(self.root).as_posix() for f in self._files(base)]
        self.naive = sum(len(f) + 1 for f in files)
        more = f"\n[{len(files) - MAX_FILES} more; narrow the path]" if self.lean and len(files) > MAX_FILES else ""
        return ("\n".join(files[:MAX_FILES] if self.lean else files) or "no files") + more

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
        return self._clip(raw)


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


SHELL_BUILTINS = {"cd", "export", "source", ".", "set", "unset", "test", "[", "true", "false", "exit", "type", "alias", "ulimit", "umask"}
if os.name == "nt":  # cmd.exe built-ins have no .exe for shutil.which to find
    SHELL_BUILTINS |= {"dir", "echo", "copy", "del", "move", "ren", "md", "mkdir", "rd", "cls", "ver", "where", "more"}


def unrunnable(cmd: str) -> str | None:
    """An error when the command's program does not exist, so a garbled call never reaches the approval prompt."""
    try:
        words = shlex.split(cmd)
    except ValueError:
        return None
    words = [w for w in words if not re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", w)] if words and "=" in words[0] else words
    if words and words[0] == "ls" and any(w == "--recursive" or (w.startswith("-") and not w.startswith("--") and "R" in w) for w in words[1:]):
        return "error: ls -R prints the whole tree. Use list_files, or search to find one."
    if not words or words[0] in SHELL_BUILTINS or "/" in words[0] or shutil.which(words[0]):
        return None
    return f"error: command not found: {words[0]}. Send a real command line such as pytest -q or git status."


def needs_approval(name: str, args: dict) -> bool:
    if name in ("read_file", "search", "list_files", "web_search"):
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
    _fn("list_files", "List repo files (skips .git, node_modules, venvs).", {"path": S}, []),
    _fn("read_file", "Read lines start..end (max 150).", {"path": S, "start": I, "end": I}, ["path"]),
    _fn("search", "Regex search, 5 hits per file.", {"pattern": S, "path": S}, ["pattern"]),
    _fn("edit_file", "Replace one unique exact string.", {"path": S, "old": S, "new": S}, ["path", "old", "new"]),
    _fn("write_file", "Create or overwrite a file.", {"path": S, "content": S}, ["path", "content"]),
    _fn("run_shell", f"Run a {SHELL} command (tests, git). Use list_files, not ls or dir.", {"cmd": CMD}, ["cmd"]),
]

WEB_SCHEMA = _fn("web_search", "Web search for current docs. Only if the repo cannot answer.", {"query": S, "max_results": I}, ["query"])


WRITE_TOOLS = ("edit_file", "write_file")


def schemas(web: bool = False, read_only: bool = False) -> list:
    tools = [t for t in SCHEMAS if not (read_only and t["function"]["name"] in WRITE_TOOLS)]
    return tools + [WEB_SCHEMA] if web else tools
