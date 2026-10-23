#!/usr/bin/env python3
"""Capture shell-invoked oracle help using an existing CLI tree inventory.

CliRunner terminal_width changes Click's usage wrapping independently of
COLUMNS; parity snapshots must use the same subprocess environment as the CLI.
"""
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("reference", type=Path)
parser.add_argument("--width", type=int, default=160)
parser.add_argument("--jobs", type=int, default=4)
args = parser.parse_args()
reference = args.reference.resolve()
commands = json.loads((reference / "inventory/cli-tree.json").read_text())
output = reference / "inventory/help"
output.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory(prefix="spec-kitty-help-reference-") as scratch:
    root = Path(scratch)
    home = root / "home"
    home.mkdir()
    environment = {
        "HOME": str(home),
        "PATH": os.environ.get("PATH", ""),
        "SPEC_KITTY_NO_UPGRADE_CHECK": "1",
        "NO_COLOR": "1",
        "TERM": "dumb",
        "COLUMNS": str(args.width),
        "LANG": "C.UTF-8",
    }

    def capture(command):
        path = command["path"].split()
        result = subprocess.run(
            [str(reference / "oracle-bin/spec-kitty"), *path[1:], "--help"],
            cwd=root, env=environment, text=True, capture_output=True, timeout=120,
        )
        if result.returncode:
            raise RuntimeError(f"{command['path']}: {result.returncode}: {result.stderr}")
        destination = output / ("_".join(path) + ".txt")
        before = destination.read_text() if destination.exists() else None
        destination.write_text(result.stdout)
        return int(before != result.stdout)

    with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as pool:
        changed = sum(pool.map(capture, commands))
print(f"Captured {len(commands)} help paths at COLUMNS={args.width}; {changed} changed.")
