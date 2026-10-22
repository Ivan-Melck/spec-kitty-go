"""Round-trip corpus for internal/rtyaml.

usage: python -I rtgen.py OUT.json [N] [SEED]
Each case: {"src": str, "out": [r0, r1, r2]} where r = {"ok": text} or
{"err": "Class|message"}: r0 plain load+dump, r1 after
payload["tracker"] = {...} (when the root is a mapping), r2 after deleting
the first key (when the root is a non-empty mapping).
Cases whose load fails are dropped.
"""
import sys, json, random, io
from ruamel.yaml import YAML
import warnings
warnings.simplefilter("ignore")

TRACKER = lambda: {
    "provider": "beads",
    "binding_ref": None,
    "project_slug": None,
    "display_label": None,
    "provider_context": None,
    "workspace": "ws",
    "ownership": {"mode": "external_authoritative", "field_owners": {}},
}

def run(src, edit):
    yaml = YAML()
    yaml.preserve_quotes = True
    data = yaml.load(src)
    if edit == 1:
        if not isinstance(data, dict):
            return None
        data["tracker"] = TRACKER()
    elif edit == 2:
        if not isinstance(data, dict) or len(data) == 0:
            return None
        del data[list(data.keys())[0]]
    buf = io.StringIO()
    try:
        yaml.dump(data, buf)
    except RecursionError:
        return {"err": "RecursionError|"}
    except Exception as e:
        return {"err": type(e).__name__ + "|" + str(e)}
    return {"ok": buf.getvalue()}

seeds = [
    "# Top comment\nagents:\n  available: [claude, codex]   # inline flow seq\n  auto_commit: true\n# between\nvcs:\n    type: \"git\"    # quoted, 4-indent\n    extra: 'single'\nlist:\n    - one   # c1\n    -   two\n    - \"three\"\nnested:\n  deep:\n    key: value\nempty:\nlong_string: >\n  folded\n  text\nlit: |\n  a\n  b\nnum: 010\ndate: 2024-01-01\nanchor: &a {x: 1}\nref: *a\n",
    "a: 1\nb: [x, y]\nc: {d: e}\n",
    "# leading\n\na: 1  # one\n\n# two\nb:\n  - x  # ex\n  # mid\n  - y\n# end\n",
    "agents:\n  available:\n  - claude\n  - codex\n  selection:\n    preferred_implementer: claude\nvcs:\n  type: git\ntracker:\n  provider: linear\n  workspace: acme\n",
    "key: \"quoted \\\" str\"\nother: 'single ''q'''\nlit: |\n  line1\n  line2\nfold: >-\n  a\n  b\n",
    "- &a {x: 1}\n- *a\n- <<: *a\n  y: 2\n",
    "base: &b\n  k: v\n  j: w\nderived:\n  <<: *b\n  j: z\n",
    "x: !!str 3\ny: !!int '7'\nz: ~\nw: yes\nv: 0o17\nu: 0x1F\nt: 1_000\ns: 3.5e+2\nr: .inf\nq: -0.5\np: 1.\no: 6.02e23\n",
    "? complex key\n: value\n? [a, b]\n: c\n",
    "a:\n  b:\n    c: [1, 2, {d: 3}]\n  e: >\n    folded\n    text that is long enough to be folded maybe\n",
    "list:\n  - one\n  -   two\n  - - nested\n    - more\n",
    "empty:\nnull_val: null\ntilde: ~\nstr: \"\"\n",
    "# comment\na: b # trailing\n\n\nc: d\n...\n",
    "ts: 2024-01-01 10:00:00 +02:00\nt2: 2024-01-01T10:00:00Z\nt3: 2024-01-01t10:00:00.5\n",
    "a: \"\\x41\\u00e9\\U0001F600\"\nb: \"line\\\n  cont\"\nc: caf\u00e9\n",
    "[a, b, {c: d}, [e]]\n",
    "{a: 1, b: [2, 3], c: {d: 4}}\n",
    "plain: multi\n  line\n  scalar\n",
    "s: 'it''s'\nt: \"tab\\tx\"\n",
    "long: " + " ".join(["word%d" % i for i in range(30)]) + "\n",
    "dq: \"" + " ".join(["word%d" % i for i in range(30)]) + "\"\n",
    "sq: '" + " ".join(["word%d" % i for i in range(30)]) + "'\n",
    "set: !!set\n  ? a\n  ? b\n",
    "om: !!omap\n- a: 1\n- b: 2\n",
    "custom: !foo bar\nc2: !foo {a: 1}\nc3: !foo [1]\n",
    "sync:\n  enabled: true   # keep\n  server: \"https://x\"\n\n# trailing block\n",
    "- a\n- b  # c\n\n- c\n",
    "a: |+\n  keep\n\nb: x\n",
    "a: |2-\n    indented\n",
    "a: >\n  one\n\n  two\n   three\n",
    "k: [a, b]  # fc\nm: {}  # em\nn: []\n",
    "\"quoted key\": 1\n'sq key': 2\n",
    "a:\n  - b: 1\n    c: 2\n  - d: 3\n",
    "a:\n- b\n- c\nd: e\n",
    "x: 0b101\ny: 0o0017\nz: 0x00FF\nw: 1_000_000\nv: 007\n",
    "f1: 1.50\nf2: 1e3\nf3: .5\nf4: -1.0E-3\nf5: +12.5\nf6: 100.\n",
    "a: &x 1\nb: *x\nc: &y hello\nd: *y\n",
    "# only comment\n",
    "",
    "a: [1, 2,\n  3]\n",
    "a: {b: c,\n  d: e}\n",
    "a: \"multi\n  line dq\"\nb: 'multi\n  line sq'\n",
    "a: b\n# c1\n# c2\n",
    "x:\n  # pre\n  y: 1\n",
    "- # comment on dash\n  a: 1\n",
    "a: 1 #no space\nb: 2\t# tab\n",
    "a:\n  b: 1\n\n\n  # gap\n  c: 2\n\nd: 3\n",
    "seq:\n- [a, b]   # x\n- {c: d}  # y\n- !!str 1\n- &q q\n- *q\n",
    "a: very long plain scalar value that should definitely be folded at the eighty column limit by the emitter\n",
    "- - - deep\n    - x\n  - y\n- z\n",
    "k:\n  - a: 1\n    # inner\n    b: 2\n  # outer\n  - c\n",
    "ver: 1.0\nx: 2024-01-01 00:00:00\ny: 2024-01-01t00:00:00+01:00\n",
    "%YAML 1.2\n---\na: 1\n",
    "--- # doc comment\na: 1\n",
    "a: 'x' # c\nb: \"y\" # d\nc: |  # e\n  z\n",
    "? a\n? b\n",
    "a: !!binary aGVsbG8=\n",
    "a: null\nb: Null\nc: NULL\nd: True\ne: FALSE\n",
    "%YAML 1.1\n---\na: 010\nb: 1:30\nc: yes\nd: 0b11\ne: 1.5:00\nf: 'on'\ng: 0x_1F\n",
    "%TAG !e! tag:example.com,2000:\n---\na: !e!foo bar\nb: !e!baz {x: 1}\n",
    "a: &id001 x\nb: *id001\nc: &anc [1, 2]\nd: *anc\ne: &m {k: v}\nf:\n  <<: *m\n  z: 1\n",
    "a:\n  <<: [*x, *y]\n",
    "x: &x {a: 1}\ny: &y {b: 2}\nz:\n  <<: [*x, *y]\n  c: 3\n",
    "a: 2001-12-14t21:59:43.10-05:00\nb: 2001-12-14 21:59:43.10 -5\nc: 2002-12-14\n",
    "a: \"\\u00e9\\t\\x07\"\nb: \"\\U0001F600 emoji\"\nc: \"\\0\"\n",
    "a: -0b101\nb: -0x1A\nc: -007\nd: +12\ne: 1__2\n",
    "a: 1e-5\nb: -1.5E+10\nc: 0.001\nd: 00.5\ne: 1.e5\nf: .1e-2\n",
    "a: !!float 42\nb: !!int 0o7\nc: !!bool yes\nd: !!null ''\n",
    "a: >-\n  long folded paragraph with many words that needs to be wrapped because it is longer than eighty chars\n",
    "- |\n  lit\n- >\n  fold\n- 'q'\n",
    "a: [1, [2, [3, [4]]]]\nb: {c: {d: {e: f}}}\n",
    "? |\n  block key\n: v\n",
    "a: \" lead\"\nb: 'trail '\nc: \"\"\nd: ''\ne: '#notcomment'\nf: ': colon'\n",
]
alphabet = list(":-[]{}'\"#&*!|>%@`, \t\n?.~0123456789abcxyz=<\u00e9")

def mutate(s, rnd):
    s = list(s)
    for _ in range(rnd.randint(1, 3)):
        op = rnd.random()
        pos = rnd.randint(0, len(s))
        if op < 0.35 and s:
            del s[min(pos, len(s) - 1)]
        elif op < 0.75:
            s.insert(pos, rnd.choice(alphabet))
        elif op < 0.85:
            s.insert(pos, rnd.choice([" # c", "\n# line\n", "\n\n", "  ", "\n  "]))
        elif op < 0.9:
            other = rnd.choice(seeds)
            a = rnd.randint(0, len(other))
            b = rnd.randint(a, min(len(other), a + 40))
            s[pos:pos] = list(other[a:b])
        elif s:
            s[min(pos, len(s) - 1)] = rnd.choice(alphabet)
    return "".join(s)

def main():
    out = sys.argv[1]
    n = int(sys.argv[2]) if len(sys.argv) > 2 else 2000
    rnd = random.Random(int(sys.argv[3]) if len(sys.argv) > 3 else 7)
    cases = list(seeds)
    while len(cases) < n:
        cases.append(mutate(rnd.choice(seeds), rnd))
    seen = set()
    rows = []
    for src in cases:
        if src in seen:
            continue
        seen.add(src)
        try:
            YAML().load(src)
        except Exception:
            continue
        except RecursionError:
            continue
        outs = [run(src, e) for e in range(3)]
        rows.append({"src": src, "out": outs})
    json.dump(rows, open(out, "w"), ensure_ascii=False, indent=0)

main()
