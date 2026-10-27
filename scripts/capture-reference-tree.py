#!/usr/bin/env python3
"""Inventory the installed oracle's CLI: every command path with its params.

Run with the oracle venv's Python:
    "$REF/oracle-venv/bin/python" -I scripts/capture-reference-tree.py "$REF"
Writes $REF/inventory/cli-tree.json. Typer may vendor its own click, so
commands and parameters are inspected by attribute, not by click class.
"""
import json
import sys
from pathlib import Path

import specify_cli
from typer.main import get_command

SCALARS = (str, int, float, bool, type(None), list)


def param_type(param):
    kind = param.type
    if hasattr(kind, "choices"):
        return {"choice": list(kind.choices)}
    return getattr(kind, "name", str(kind))


def describe_param(param):
    return {
        "kind": param.param_type_name,
        "name": param.name,
        "opts": list(param.opts) + list(getattr(param, "secondary_opts", [])),
        "type": param_type(param),
        "required": param.required,
        "default": param.default if isinstance(param.default, SCALARS) else repr(param.default),
        "multiple": param.multiple,
        "nargs": param.nargs,
        "is_flag": getattr(param, "is_flag", False),
        "hidden": getattr(param, "hidden", False),
        "help": getattr(param, "help", None),
        "envvar": param.envvar,
    }


def walk(command, path, hidden_parent, out):
    hidden = hidden_parent or bool(command.hidden)
    callback = command.callback
    out.append({
        "path": " ".join(path),
        "is_group": hasattr(command, "commands"),
        "hidden": bool(command.hidden),
        "hidden_effective": hidden,
        "deprecated": bool(getattr(command, "deprecated", False)),
        "help": command.help or "",
        "short_help": command.short_help or "",
        "invoke_without_command": bool(getattr(command, "invoke_without_command", False)),
        "cls": type(command).__name__,
        "callback": f"{callback.__module__}.{callback.__qualname__}" if callback else None,
        "params": [describe_param(p) for p in command.params],
    })
    for name, child in getattr(command, "commands", {}).items():
        walk(child, [*path, name], hidden, out)


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: capture-reference-tree.py REF")
    commands = []
    walk(get_command(specify_cli._get_app()), ["spec-kitty"], False, commands)
    inventory = Path(sys.argv[1]) / "inventory"
    inventory.mkdir(parents=True, exist_ok=True)
    (inventory / "cli-tree.json").write_text(json.dumps(commands, indent=2, default=str) + "\n")
    print(f"Captured {len(commands)} command paths.")


if __name__ == "__main__":
    main()
