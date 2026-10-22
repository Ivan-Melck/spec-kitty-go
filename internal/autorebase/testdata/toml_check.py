"""Report, per TOML document on stdin (a JSON list), whether tomllib accepts it."""
import json
import sys
import tomllib

out = []
for doc in json.load(sys.stdin):
    try:
        tomllib.loads(doc)
        out.append(True)
    except tomllib.TOMLDecodeError:
        out.append(False)
json.dump(out, sys.stdout)
