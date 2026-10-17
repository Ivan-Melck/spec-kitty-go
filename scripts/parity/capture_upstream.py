"""Capture immutable upstream contract facts without importing or running upstream.

This is development-only inventory tooling, not a Go runtime dependency or a
conformance test. It never fetches, checks out, or modifies the reference repo.
"""

import argparse
import ast
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile


SOURCE_PATHS = (
    "src/specify_cli/__init__.py",
    "src/specify_cli/_completion_manifest.json",
    "src/specify_cli/core/upstream_contract.json",
    "src/specify_cli/orchestrator_api/commands.py",
    "src/specify_cli/orchestrator_api/envelope.py",
    "src/specify_cli/orchestrator_api/design_authoring.py",
    "src/specify_cli/orchestrator_api/design_context.py",
    "src/specify_cli/orchestrator_api/design_phase.py",
    "src/specify_cli/orchestrator_api/runtime_next.py",
    "src/specify_cli/cli/commands/next_cmd.py",
    "src/runtime/next/decision.py",
)


def git(repo, *args):
    return subprocess.run(
        ["git", "--no-replace-objects", "-C", str(repo), *args], check=True, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    ).stdout


def literal_constant(source, name):
    for node in ast.parse(source).body:
        if isinstance(node, ast.Assign) and any(
            isinstance(target, ast.Name) and target.id == name for target in node.targets
        ):
            value = ast.literal_eval(node.value)
            if not isinstance(value, str) or not value:
                raise ValueError(f"{name} is not a nonempty string constant")
            return value
    raise ValueError(f"missing literal {name}")


def capture(repo, revision):
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("revision must be a full lowercase commit SHA, not main/latest/a tag")
    resolved = git(repo, "rev-parse", "--verify", f"{revision}^{{commit}}").decode().strip()
    if resolved != revision:
        raise ValueError("revision does not resolve to the exact requested commit")
    sources = {path: git(repo, "show", f"{revision}:{path}") for path in SOURCE_PATHS}
    contract = json.loads(sources["src/specify_cli/core/upstream_contract.json"])
    completion = json.loads(sources["src/specify_cli/_completion_manifest.json"])
    envelope = sources["src/specify_cli/orchestrator_api/envelope.py"].decode("utf-8")
    api = contract["orchestrator_api"]
    commands = api["allowed_commands"]
    if not commands or len(commands) != len(set(commands)):
        raise ValueError("upstream command census is empty or contains duplicates")
    return {
        "schema_version": 1,
        "purpose": "source inventory only; not executable compatibility evidence",
        "upstream_repository": "https://github.com/spec-kitty/spec-kitty",
        "upstream_commit": revision,
        "upstream_tree": git(repo, "rev-parse", f"{revision}^{{tree}}").decode().strip(),
        "source_sha256": {
            path: hashlib.sha256(data).hexdigest() for path, data in sources.items()
        },
        "orchestrator_api": {
            "contract_version": literal_constant(envelope, "CONTRACT_VERSION"),
            "min_provider_version": literal_constant(envelope, "MIN_PROVIDER_VERSION"),
            "allowed_commands": sorted(commands),
            "forbidden_commands": sorted(api["forbidden_commands"]),
            "allowed_error_codes": sorted(api["allowed_error_codes"]),
        },
        "root_completion_commands": {
            name: {"hidden": item["hidden"], "deprecated": item["deprecated"]}
            for name, item in sorted(completion["commands"].items())
            if not name.startswith("__")
        },
    }


def protect_reference(repo, output):
    # Linked worktrees have separate Git and common directories. Resolve
    # symlink aliases before comparing so an output cannot overwrite source,
    # refs, objects or configuration in either repository directory.
    roots = [git(repo, "rev-parse", "--show-toplevel").decode().strip()]
    for kind in ("--git-dir", "--git-common-dir"):
        roots.append(git(repo, "rev-parse", "--path-format=absolute", kind).decode().strip())
    target = output.resolve()
    if any(target.is_relative_to(Path(root).resolve()) for root in roots):
        raise ValueError("output must be outside the reference worktree and Git directories")


def write_snapshot(output, encoded):
    output.parent.mkdir(parents=True, exist_ok=True)
    # Replace the destination inode rather than truncate it: an existing hard
    # link must not turn an outside output into a reference-source mutation.
    with tempfile.NamedTemporaryFile(dir=output.parent, prefix=".upstream-inventory-", delete=False) as file:
        temporary = Path(file.name)
        try:
            file.write(encoded)
            file.close()
            temporary.replace(output)
        finally:
            temporary.unlink(missing_ok=True)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--upstream", type=Path, required=True)
    parser.add_argument("--revision", required=True, help="full immutable upstream commit SHA")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--check", action="store_true", help="verify the snapshot without writing")
    args = parser.parse_args(argv)
    try:
        protect_reference(args.upstream, args.output)
        snapshot = capture(args.upstream, args.revision)
        encoded = (json.dumps(snapshot, indent=2, sort_keys=True, ensure_ascii=False) + "\n").encode()
        if args.check:
            if args.output.read_bytes() != encoded:
                print("Upstream inventory differs; reconcile the baseline explicitly.", file=sys.stderr)
                return 1
        else:
            write_snapshot(args.output, encoded)
    except (OSError, ValueError, KeyError, SyntaxError, subprocess.CalledProcessError) as error:
        print(f"Upstream capture refused: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
