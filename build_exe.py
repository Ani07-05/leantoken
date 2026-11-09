"""Build a single-file leantoken executable into dist/. Run: python build_exe.py"""

import PyInstaller.__main__

ENTRY = "build/entry.py"

if __name__ == "__main__":
    import os

    os.makedirs("build", exist_ok=True)
    with open(ENTRY, "w") as f:
        f.write("from leantoken.cli import main\nmain()\n")
    PyInstaller.__main__.run([
        ENTRY, "--onefile", "--name", "leantoken", "--console",
        "--collect-all", "textual",  # Textual loads CSS and widgets dynamically
        "--distpath", "dist", "--workpath", "build", "--specpath", "build", "--noconfirm",
    ])
