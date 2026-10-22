"""list.sort() with a deliberately inconsistent __lt__ (JSON lines).

Each case: n items, a random "less than" table and the permutation CPython's
timsort produces, so the Go port can be compared comparison-for-comparison.
"""
import json
import random
import sys

rng = random.Random(int(sys.argv[2]))


class Item:
    __slots__ = ("i", "table")

    def __init__(self, i, table):
        self.i = i
        self.table = table

    def __lt__(self, other):
        return self.table[self.i][other.i]


for case in range(int(sys.argv[1])):
    n = rng.choice([2, 3, 5, 10, 40, 63, 64, 65, 100, 150, 300])
    mode = case % 3
    keys = [rng.randint(0, n // 3 + 1) for _ in range(n)]
    table = []
    for a in range(n):
        row = []
        for b in range(n):
            if mode == 0:
                row.append(rng.random() < 0.5)
            elif mode == 1:
                row.append(keys[a] < keys[b])
            else:
                row.append(keys[a] < keys[b] if rng.random() < 0.9 else rng.random() < 0.5)
        table.append(row)
    items = [Item(i, table) for i in range(n)]
    items.sort()
    print(json.dumps({"table": [[1 if x else 0 for x in r] for r in table], "perm": [it.i for it in items]}))
