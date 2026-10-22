import json, sys
out = sys.argv[1]; parts = [json.load(open(p)) for p in sys.argv[2:]]
rows = parts[0]
for p in parts[1:]:
    for r, q in zip(rows, p):
        assert r["src"] == q["src"]
        r["out"].update(q["out"])
json.dump(rows, open(out, "w"), ensure_ascii=False, indent=0)
