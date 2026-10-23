#!/usr/bin/env python3
"""Regenerate the Go glossary producer contracts using the pinned oracle venv.

Run with $REF/oracle-venv/bin/python scripts/sync-event-contracts.py [--check].
This is a development tool; the Go executable has no Python dependency.
"""
import argparse
import importlib.metadata
import json
from pathlib import Path

from spec_kitty_events.conformance.validators import _EVENT_TYPE_TO_MODEL, _EVENT_TYPE_TO_SCHEMA
from spec_kitty_events.schemas import load_schema

PRODUCERS = ("TermCandidateObserved", "GlossaryClarificationResolved", "GlossarySenseUpdated")


def trim(value):
    if isinstance(value, dict):
        return {key: trim(item) for key, item in value.items()
                if key not in ("description", "title", "$id", "$schema")}
    if isinstance(value, list):
        return [trim(item) for item in value]
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    version = importlib.metadata.version("spec-kitty-events")
    if version != "11.0.0":
        parser.error(f"expected spec-kitty-events 11.0.0 from the RC8 oracle, found {version}")
    contracts = {name: {"model": trim(_EVENT_TYPE_TO_MODEL[name].model_json_schema()),
                        "schema": trim(load_schema(_EVENT_TYPE_TO_SCHEMA[name]))}
                 for name in PRODUCERS}
    content = json.dumps(contracts, ensure_ascii=True, indent=2) + "\n"
    target = Path(__file__).resolve().parent.parent / "internal/glossary/ledger_contracts.json"
    if args.check:
        if target.read_text() != content:
            parser.exit(1, f"outdated event contracts: {target}\n")
    else:
        target.write_text(content)


if __name__ == "__main__":
    main()
