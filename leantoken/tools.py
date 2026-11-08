import re
import shutil
import subprocess
from pathlib import Path

MAX_OUT = 3000
SAFE_CMD = re.compile(r"^(git (status|diff|log|show|branch)|ls|pwd|cat|head|tail|wc)\b[^;&|><`$]*$")


def clip(s: str, n: int = MAX_OUT) -> str:
    if len(s) <= n:
        return s
    return s[: n // 2] + f"\n...[{len(s) - n} chars cut]...\n" + s[-n // 2 :]


class Workspace:
    def __init__(self, root: Path):
        self.root = root.resolve()

    def _path(self, p: str) -> Path:
        full = (self.root / p).resolve()
        if self.root != full and self.root not in full.parents:
            raise ValueError(f"path escapes repo: {p}")
        return full

    def read_file(self, path: str, start: int = 1, end: int = 200) -> str:
        lines = self._path(path).read_text(errors="replace").splitlines()
        chunk = lines[max(start - 1, 0) : end]
        body = "\n".join(f"{i}\t{l}" for i, l in enumerate(chunk, start))
        return clip(body + f"\n[{len(lines)} lines total]")

    def search(self, pattern: str, path: str = ".") -> str:
        target = str(self._path(path))
        if shutil.which("rg"):
            cmd = ["rg", "-n", "--no-heading", "-m", "5", pattern, target]
        else:
            cmd = ["grep", "-rn", "-m", "5", pattern, target]
        out = subprocess.run(cmd, capture_output=True, text=True, cwd=self.root).stdout
        return clip(out.replace(str(self.root) + "/", "") or "no matches")

    def write_file(self, path: str, content: str) -> str:
        p = self._path(path)
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content)
        return f"wrote {path} ({len(content)} bytes)"

    def edit_file(self, path: str, old: str, new: str) -> str:
        p = self._path(path)
        text = p.read_text()
        if text.count(old) != 1:
            return f"error: old string matched {text.count(old)} times, need exactly 1"
        p.write_text(text.replace(old, new))
        return f"edited {path}"

    def run_shell(self, cmd: str) -> str:
        r = subprocess.run(cmd, shell=True, capture_output=True, text=True, cwd=self.root, timeout=120)
        return clip(f"exit={r.returncode}\n{r.stdout}{r.stderr}")


def needs_approval(name: str, args: dict) -> bool:
    if name in ("read_file", "search"):
        return False
    if name == "run_shell":
        return not SAFE_CMD.match(args.get("cmd", ""))
    return True


def describe(name: str, args: dict) -> str:
    if name == "edit_file":
        return f"edit {args['path']}\n- {args['old']}\n+ {args['new']}"
    if name == "write_file":
        return f"write {args['path']} ({len(args['content'])} bytes)"
    return f"shell: {args.get('cmd')}"


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
SCHEMAS = [
    _fn("read_file", "Read a line range of a file.", {"path": S, "start": I, "end": I}, ["path"]),
    _fn("search", "Regex search, max 5 hits per file.", {"pattern": S, "path": S}, ["pattern"]),
    _fn("edit_file", "Replace one exact unique string in a file.", {"path": S, "old": S, "new": S}, ["path", "old", "new"]),
    _fn("write_file", "Create or overwrite a file.", {"path": S, "content": S}, ["path", "content"]),
    _fn("run_shell", "Run a shell command (tests, git).", {"cmd": S}, ["cmd"]),
]
