"""Differential corpus for internal/pytoml: python -I tgen.py OUT N SEED"""
import sys, json, random, datetime, math, tomllib

def canon(v):
    if isinstance(v, bool):
        return {"bool": v}
    if isinstance(v, int):
        return {"int": str(v)}
    if isinstance(v, float):
        if math.isnan(v):
            return {"float": "nan"}
        return {"float": repr(v)}
    if isinstance(v, datetime.datetime):
        return {"dt": v.isoformat()}
    if isinstance(v, datetime.date):
        return {"date": v.isoformat()}
    if isinstance(v, datetime.time):
        return {"time": v.isoformat()}
    if isinstance(v, str):
        return v
    if isinstance(v, dict):
        return {"map": [[k, canon(x)] for k, x in v.items()]}
    if isinstance(v, list):
        return [canon(x) for x in v]
    return {"other": repr(v)}

seeds = [
    '[gateway]\ntoken = "abc"\nexpires = 1700000000\n',
    'title = "TOML"\n[owner]\nname = "Tom"\ndob = 1979-05-27T07:32:00-08:00\n',
    '[database]\nports = [ 8000, 8001, 8002 ]\nenabled = true\ntemp = { cpu = 79.5, case = 72.0 }\n',
    '[[products]]\nname = "Hammer"\nsku = 738594937\n[[products]]\n[[products]]\nname = "Nail"\ncolor = "gray"\n',
    'a.b.c = 1\na.d = 2\n[x.y]\nz = 3\n',
    's1 = """\nmulti\nline"""\ns2 = \'\'\'raw\\nstr\'\'\'\ns3 = "esc \\t \\u00e9 \\U0001F600"\n',
    'i1 = +99\ni2 = -17\ni3 = 1_000\nh = 0xDEAD_BEEF\no = 0o755\nb = 0b1101\nf1 = 3.1415\nf2 = -0.01\nf3 = 5e+22\nf4 = 1e06\nf5 = -2E-2\nf6 = 6.626e-34\nf7 = inf\nf8 = -nan\n',
    'ld = 1979-05-27\nlt = 07:32:00.999999\nldt = 1979-05-27 07:32:00Z\n',
    '"quoted key" = 1\n\'literal key\' = 2\nbare-key_1 = 3\n',
    'arr = [\n  1, # c\n  2,\n]\nnested = [[1, 2], ["a"]]\n',
    '[tracker.beads]\ncommand = "/usr/bin/bd"\nworkspace = "w"\n',
    'a = 1\na = 2\n',
    '[t]\nx = 1\n[t]\ny = 2\n',
    't = {a = 1}\nt.b = 2\n',
]
alphabet = list('[]{}"\'=.,#\n \t\\-_+:0123456789abcxyzTZe')

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
    out, n, seed = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
    rnd = random.Random(seed)
    cases = list(seeds)
    while len(cases) < n:
        cases.append(mutate(rnd.choice(seeds), rnd))
    rows, seen = [], set()
    for src in cases:
        if src in seen:
            continue
        seen.add(src)
        try:
            r = {"val": canon(tomllib.loads(src))}
        except tomllib.TOMLDecodeError as e:
            r = {"err": str(e)}
        except Exception as e:
            r = {"exc": type(e).__name__ + "|" + str(e)}
        rows.append({"src": src, "res": r})
    json.dump(rows, open(out, "w"), ensure_ascii=False)

main()
