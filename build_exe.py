"""Build a single-file slendertoken executable into dist/. Run: python build_exe.py"""

import PyInstaller.__main__

ENTRY = "build/entry.py" if __name__ == "__main__":
    import os

    os.makedirs("build", exist_ok=True)
    with open(ENTRY, "w") as f:
        f.write("from slendertoken.cli import main\nmain()\n")
    PyInstaller.__main__.run([
        ENTRY, "--onefile", "--name", "slendertoken", "--console",
        "--collect-all", "textual",  # Textual loads CSS and widgets dynamically
        "--distpath", "dist", "--workpath", "build", "--specpath", "build", "--noconfirm",
    ])
