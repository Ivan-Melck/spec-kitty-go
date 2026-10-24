#!/usr/bin/env python3
"""Inventory the installed oracle's CLI (run with its venv's Python)."""
import argparse
import json
from pathlib import Path

import specify_cli
from typer.main import get_command

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("reference", type=Path)
args = parser.parse_args()
commands = []


def walk(command, path):
    commands.append({
        "path": " ".join(path),
        "is_group": hasattr(command, "commands"),
        "hidden": bool(command.hidden),
        "help": command.help or "",
    })
    for name, child in getattr(command, "commands", {}).items():
        walk(child, [*path, name])


walk(get_command(specify_cli._get_app()), ["spec-kitty"])
inventory = args.reference / "inventory"
inventory.mkdir(parents=True, exist_ok=True)
(inventory / "cli-tree.json").write_text(json.dumps(commands, indent=2) + "\n")
print(f"Captured {len(commands)} command paths.")
