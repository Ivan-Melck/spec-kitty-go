"""Generate a differential corpus for the Go PyYAML/ruamel port.

usage: python -I ygen.py OUT.json [N]
Each case: {"src": str, "pyyaml": result, "ruamel_safe": result, "ruamel_rt": result}
result: {"err": "Class|message"} or {"val": canonical}
"""
import sys, json, random, datetime, math
import yaml
from ruamel.yaml import YAML
import warnings
warnings.simplefilter("ignore")

def canon(v):
    if type(v).__name__ == "CommentedSet":
        return {"set": sorted(keystr(k) for k in v)}
    if type(v).__name__ == "TaggedScalar":
        return v.value
    if v is None:
        return None
    if isinstance(v, bool):
        return {"bool": v}
    if isinstance(v, int):
        return {"int": str(int(v))}
    if isinstance(v, float):
        if math.isnan(v):
            return {"float": "nan"}
        return {"float": float.__repr__(float(v))}
    if isinstance(v, datetime.datetime):
        return {"ts": v.isoformat(" ")}
    if isinstance(v, datetime.date):
        return {"date": v.isoformat()}
    if isinstance(v, str):
        return v
    if isinstance(v, bytes):
        return {"bytes": v.hex()}
    if isinstance(v, dict):
        return {"map": [[keystr(k), canon(x)] for k, x in v.items()]}
    if isinstance(v, (list, tuple)):
        return [canon(x) for x in v]
    if isinstance(v, set):
        return {"set": sorted(keystr(k) for k in v)}
    return {"other": type(v).__name__ + ":" + str(v)}

def keystr(k):
    if isinstance(k, str):
        return k
    if isinstance(k, bool):
        return "True" if k else "False"
    if k is None:
        return "None"
    if isinstance(k, datetime.date) and not isinstance(k, datetime.datetime):
        return k.isoformat()
    if isinstance(k, tuple):
        return str(k)
    return str(k)

def res(fn, src):
    try:
        return {"val": canon(fn(src))}
    except RecursionError:
        return {"err": "RecursionError|"}
    except Exception as e:
        return {"err": type(e).__name__ + "|" + str(e)}

def py(src):
    return yaml.safe_load(src)

def rsafe(src):
    return YAML(typ="safe", pure=True).load(src)

def rrt(src):
    return YAML(typ="rt").load(src)

seeds = [
    "a: 1\nb: [x, y]\nc: {d: e}\n",
    "---\ntitle: Foo\nlane: planned\nsubtasks:\n- T001\n- T002\nhistory:\n  - at: 2024-01-02T03:04:05Z\n    by: me\n",
    "key: \"quoted \\\" str\"\nother: 'single ''q'''\nlit: |\n  line1\n  line2\nfold: >-\n  a\n  b\n",
    "- &a {x: 1}\n- *a\n- <<: *a\n  y: 2\n",
    "%YAML 1.1\n%TAG !e! tag:example.com,2000:\n---\n!e!foo bar\n",
    "x: !!str 3\ny: !!int '7'\nz: ~\nw: yes\nv: 0o17\nu: 0x1F\nt: 1_000\ns: 3.5e+2\nr: .inf\n",
    "? complex key\n: value\n? [a, b]\n: c\n",
    "a:\n  b:\n    c: [1, 2, {d: 3}]\n  e: >\n    folded\n    text\n",
    "list:\n  - one\n  -   two\n  - - nested\n    - more\n",
    "empty:\nnull_val: null\ntilde: ~\nstr: \"\"\n",
    "# comment\na: b # trailing\n\n\nc: d\n...\n",
    "date: 2024-02-30\nts: 2024-01-01 10:00:00 +02:00\n",
    "a: \"\\x41\\u00e9\\U0001F600\"\nb: \"line\\\n  cont\"\n",
    "anchors: &anc\n  k: v\nref: *anc\n",
    "[a, b, {c: d}, [e]]\n",
    "{a: 1, b: [2, 3], c: {d: 4}}\n",
    "a: 1\na: 2\n",
    "plain: multi\n  line\n  scalar\n",
    "a: b\n\tc: d\n",
    "s: 'it''s'\nt: \"tab\\tx\"\n",
]
alphabet = list(":-[]{}'\"#&*!|>%@`, \t\n?.~0123456789abcxyz=<")

def mutate(s, rnd):
    s = list(s)
    for _ in range(rnd.randint(1, 3)):
        op = rnd.random()
        pos = rnd.randint(0, len(s))
        if op < 0.4 and s:
            del s[min(pos, len(s) - 1)]
        elif op < 0.8:
            s.insert(pos, rnd.choice(alphabet))
        elif s:
            s[min(pos, len(s) - 1)] = rnd.choice(alphabet)
    return "".join(s)

def main():
    out = sys.argv[1]
    n = int(sys.argv[2]) if len(sys.argv) > 2 else 2000
    rnd = random.Random(int(sys.argv[4]) if len(sys.argv) > 4 else 42)
    cases = list(seeds)
    extra_seeds = json.load(open(sys.argv[3])) if len(sys.argv) > 3 else []
    pool = seeds + extra_seeds
    extra = extra_seeds
    cases += extra
    while len(cases) < n + len(seeds) + len(extra):
        cases.append(mutate(rnd.choice(pool), rnd))
    seen = set()
    rows = []
    for src in cases:
        if src in seen:
            continue
        seen.add(src)
        rows.append({"src": src, "pyyaml": res(py, src), "ruamel_safe": res(rsafe, src), "ruamel_rt": res(rrt, src)})
    json.dump(rows, open(out, "w"), ensure_ascii=False, indent=0)

main()
