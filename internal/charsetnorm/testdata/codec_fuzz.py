"""Emit codec decode samples (strict + errors="ignore") as JSON lines.

Used by codec_oracle_test.go with the oracle venv's CPython.
"""
import json
import random
import sys

encs = sys.argv[1].split(",")
n = int(sys.argv[2])
rng = random.Random(int(sys.argv[3]) if len(sys.argv) > 3 else 7)

ranges = [(0x20, 0x7E), (0xA0, 0x24F), (0x370, 0x3FF), (0x400, 0x4FF), (0x590, 0x6FF), (0xE00, 0xE7F),
          (0x3000, 0x30FF), (0x4E00, 0x9FFF), (0xAC00, 0xD7A3), (0xFF00, 0xFFEF), (0x20000, 0x2A6DF), (0x2000, 0x206F)]


def rand_text(k):
    out = []
    for _ in range(k):
        a, b = rng.choice(ranges)
        out.append(chr(rng.randint(a, b)))
    return "".join(out)


def cps(s):
    return [ord(c) for c in s]


for enc in encs:
    for i in range(n):
        mode = i % 5
        if mode == 4:
            toks = [b"\x1b", b"$", b"(", b")", b".", b"&@", b"@", b"A", b"B", b"C", b"D", b"F", b"I", b"J", b"N", b"O",
                    b"P", b"Q", b"\x0e", b"\x0f", b"\n", b"~", b"~{", b"~}", b"+", b"-", b"/", b"!", b"0", b"x",
                    bytes([rng.randint(0x80, 0xFF)]), bytes([rng.randint(0x21, 0x7E)]), bytes([rng.randint(0x21, 0x7E)])]
            data = b"".join(rng.choice(toks) for _ in range(rng.randint(0, 30)))
        elif mode == 0:
            data = bytes(rng.randint(0, 255) for _ in range(rng.randint(0, 40)))
        else:
            t = rand_text(rng.randint(1, 30))
            data = t.encode(enc, errors="ignore")
            if mode >= 2 and data:
                b = bytearray(data)
                for _ in range(rng.randint(1, 3)):
                    op = rng.randint(0, 2)
                    p = rng.randint(0, len(b))
                    if op == 0 and p < len(b):
                        del b[p]
                    elif op == 1:
                        b.insert(p, rng.randint(0, 255))
                    elif p < len(b):
                        b[p] = rng.randint(0, 255)
                data = bytes(b)
            if mode == 3:
                data = data[rng.randint(0, 3):]
        try:
            strict = cps(data.decode(enc))
        except UnicodeDecodeError:
            strict = None
        ignore = cps(data.decode(enc, errors="ignore"))
        print(json.dumps({"enc": enc, "data": data.hex(), "strict": strict, "ignore": ignore}))
