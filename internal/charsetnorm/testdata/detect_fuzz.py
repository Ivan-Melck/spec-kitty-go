"""Emit charset_normalizer.from_bytes results for generated samples (JSON lines).

argv: n seed [file ...]   (files are added verbatim before the generated samples)
"""
import json
import random
import sys

from charset_normalizer import from_bytes
from charset_normalizer.constant import FREQUENCIES
from charset_normalizer.api import IANA_SUPPORTED_MB_FIRST

n = int(sys.argv[1])
rng = random.Random(int(sys.argv[2]))
files = sys.argv[3:]

ENCS = sorted(set(IANA_SUPPORTED_MB_FIRST))
LANGS = list(FREQUENCIES)
PUNCT = [" ", " ", " ", " ", ", ", ". ", "\n", " - ", "; ", ": ", "! ", "? ", " (", ") ", "\"", "'"]


def words(lang, k):
    chars = FREQUENCIES[lang]
    weights = [1.0 / (i + 1) ** 0.7 for i in range(len(chars))]
    out = []
    for _ in range(k):
        wl = rng.randint(1, 9)
        w = "".join(rng.choices(chars, weights, k=wl))
        if rng.random() < 0.1:
            w = w.capitalize()
        out.append(w)
        out.append(rng.choice(PUNCT))
    return "".join(out)


def sample():
    kind = rng.random()
    if kind < 0.08:
        return bytes(rng.randint(0, 255) for _ in range(rng.randint(1, 300)))
    if kind < 0.12:
        # mostly ascii with a few stray high bytes
        b = bytearray(words("English", rng.randint(5, 200)).encode())
        for _ in range(rng.randint(1, 4)):
            b.insert(rng.randint(0, len(b)), rng.randint(0x80, 0xFF))
        return bytes(b)
    lang = rng.choice(LANGS)
    text = words(lang, rng.choice([2, 5, 20, 60, 200, 600, 1500]))
    if rng.random() < 0.3:
        text = words("English", rng.randint(3, 40)) + text
    for _ in range(20):
        enc = rng.choice(ENCS)
        if enc.startswith("utf") and rng.random() < 0.7:
            continue
        try:
            data = text.encode(enc)
        except (UnicodeEncodeError, LookupError):
            continue
        if rng.random() < 0.1 and len(data) > 4:
            b = bytearray(data)
            b[rng.randint(0, len(b) - 1)] = rng.randint(0x80, 0xFF)
            data = bytes(b)
        return data
    return text.encode("utf_8")


def describe(data):
    ms = from_bytes(data)
    out = []
    for m in ms:
        out.append({
            "encoding": m.encoding,
            "chaos": m.chaos,
            "coherence": m.coherence,
            "languages": [[l, r] for l, r in m._languages],
            "could_be": m.could_be_from_charset,
            "language": m.language,
        })
    best = ms.best()
    return {"data": data.hex(), "results": out, "best": best.encoding if best else None}


def describe_recover(data):
    from charter.encoding_recovery import recover

    out = {"data": data.hex()}
    for unsafe in (False, True):
        try:
            r = recover(data, unsafe=unsafe)
            out["unsafe" if unsafe else "safe"] = {
                "text": [ord(c) for c in r.text] if r.text is not None else None,
                "source_encoding": r.source_encoding,
                "confidence": r.confidence,
                "ambiguous": r.ambiguous,
                "candidates": [[e, c] for e, c in r.candidates],
                "normalization_applied": r.normalization_applied,
                "bypass_used": r.bypass_used,
            }
        except UnicodeDecodeError as exc:
            notes = "".join("\n[NOTE] " + n for n in getattr(exc, "__notes__", []))
            out["unsafe" if unsafe else "safe"] = {"error": str(exc) + notes}
    return out


fn = describe_recover if "--recover" in files else describe
files = [f for f in files if f != "--recover"]
for f in files:
    with open(f, "rb") as fh:
        print(json.dumps(fn(fh.read())))
for _ in range(n):
    print(json.dumps(fn(sample())))
