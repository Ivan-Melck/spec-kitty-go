"""Run the upstream classifier over JSON cases (stdin) and print results.

Each case is {"file": path, "body": conflicted file text}; the output
mirrors autorebase.ResolveText: {"rebuilt", "rules", "halt"}.
"""
import json
import sys
from pathlib import Path

from specify_cli.consolidation.conflict_classifier import Auto, Manual, classify, validate_resolution
from specify_cli.lanes.auto_rebase import _split_into_regions, _splice_resolutions

out = []
for case in json.load(sys.stdin):
    path = Path(case["file"])
    segments, count = _split_into_regions(case["body"])
    res = {"rebuilt": "", "rules": [], "halt": ""}
    if count == 0:
        res["halt"] = f"file {path} marked conflicted but contains no conflict markers"
        out.append(res)
        continue
    cls = []
    halt = None
    for is_conflict, seg in segments:
        if not is_conflict:
            continue
        c = classify(path, seg)
        cls.append(c)
        if isinstance(c.resolution, Manual):
            halt = c.resolution.reason
            break
    if halt is None:
        rebuilt = _splice_resolutions(segments, cls)
        validated = []
        for c in cls:
            v = validate_resolution(c, rebuilt)
            validated.append(v)
            if isinstance(v.resolution, Manual):
                halt = v.resolution.reason
                break
        cls = validated
        if halt is None:
            res["rebuilt"] = rebuilt
    res["rules"] = [c.resolution.rule_id if isinstance(c.resolution, Auto) else "MANUAL" for c in cls]
    res["halt"] = halt or ""
    out.append(res)
json.dump(out, sys.stdout)
