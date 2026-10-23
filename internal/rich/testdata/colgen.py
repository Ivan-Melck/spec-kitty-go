"""Golden corpus for internal/rich colour rendering (console.print with
markup + ReprHighlighter) for each colour system. usage: colgen.py OUT N SEED"""
import io, json, random, sys
from rich.console import Console

words = ["hello", "Mission", "WP01", "42", "3.14", "-7", "1e5", "0x1F", "True", "False", "None",
         "'quoted'", '"dq"', "/tmp/x/file.txt", "src/a.py", "https://example.com/a?b=1", "...",
         "(a, b)", "[1, 2]", "{x: 1}", "fn(x)", "k=v", "<tag a=1>", "192.168.0.1",
         "123e4567-e89b-12d3-a456-426614174000", "café", "→", "x_1", "v2.0.1", "1+2j", "", "a.0.b", "1.2.3", "3.14e5x", "x=1", "k=\"v\"", "<a b>", "foo(1)", "None.", "-5.", "0x1fz", "2001:db8::1", "aa:bb:cc:dd:ee:ff", "C:\\x", "\"a\"b", "it's", "e1", "1e", "1_000"]
tags = ["bold", "red", "green", "cyan", "yellow", "dim", "bold red", "italic blue", "#ff8800",
        "color(200)", "rgb(10,200,30)", "on blue", "white on red", "not bold", "underline",
        "bright_magenta", "grey50", "reverse", "strike", "b i u"]

def gen(rnd):
    parts = []
    for _ in range(rnd.randint(1, 20)):
        r = rnd.random()
        if r < 0.3:
            t = rnd.choice(tags)
            name = t.split("=")[0]
            inner = " ".join(rnd.choice(words) for _ in range(rnd.randint(1, 3)))
            close = "[/]" if rnd.random() < 0.3 else f"[/{name}]"
            parts.append(f"[{t}]{inner}{close}")
        else:
            parts.append(rnd.choice(words))
    return " ".join(parts)

def main():
    out, n, seed = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
    only = sys.argv[4]  # one colour system per process: rich caches SGR codes per Style
    rnd = random.Random(seed)
    rows = []
    for i in range(n):
        s = gen(rnd)
        res = {}
        for sysname in [only]:
            buf = io.StringIO()
            c = Console(file=buf, force_terminal=True, color_system=sysname, width=80)
            try:
                c.print(s)
                res[sysname] = buf.getvalue()
            except Exception as e:
                res[sysname] = "ERR " + type(e).__name__
        rows.append({"src": s, "out": res})
    json.dump(rows, open(out, "w"), ensure_ascii=False, indent=0)

main()
