"""Golden corpus for internal/rich renderables (Table, Panel, Tree, Padding,
Columns) rendered by rich on a forced terminal for each colour system and
without colour. usage: rendergen.py OUT N SEED"""
import io, json, random, sys
from rich import box as rbox
from rich.columns import Columns
from rich.console import Console
from rich.padding import Padding
from rich.panel import Panel
from rich.table import Table
from rich.tree import Tree

WORDS = ["alpha", "beta", "WP01", "42", "3.14", "lane-a", "/tmp/x.py", "'q'", "True", "None",
         "café", "→", "done", "planned", "[bold]b[/bold]", "[red]r[/red]", "x" * 30, "", "a b c d e f"]
STYLES = ["", "bold", "cyan", "dim", "bold magenta", "red", "green", "yellow", "white", "on blue", "italic"]
BOXES = ["HEAVY_HEAD", "ROUNDED", "SQUARE", "SIMPLE", None]


def text(r):
    return " ".join(r.choice(WORDS) for _ in range(r.randint(1, 4))).strip()


def gen(r):
    k = r.choice(["table", "table", "panel", "tree", "padding", "columns"])
    if k == "table":
        n = r.randint(1, 4)
        cols = []
        for _ in range(n):
            c = {"header": text(r), "style": r.choice(STYLES), "justify": r.choice(["left", "right", "center"]),
                 "no_wrap": r.random() < 0.2}
            if r.random() < 0.2:
                c["width"] = r.randint(3, 15)
            if r.random() < 0.15:
                c["min_width"] = r.randint(3, 12)
            if r.random() < 0.1:
                c["ratio"] = r.randint(1, 3)
            cols.append(c)
        rows = [[text(r) for _ in range(n)] for _ in range(r.randint(0, 4))]
        return {"kind": k, "title": text(r) if r.random() < 0.5 else "", "columns": cols, "rows": rows,
                "box": r.choice(BOXES), "show_lines": r.random() < 0.3, "show_header": r.random() < 0.85,
                "show_edge": r.random() < 0.85, "expand": r.random() < 0.3, "header_style": r.choice(STYLES + ["table.header"]),
                "border_style": r.choice(STYLES), "padding": r.choice([[0, 1], [0, 2], [0, 0], [1, 1]]),
                "pad_edge": r.random() < 0.8, "highlight": r.random() < 0.3,
                "row_style": r.choice(STYLES), "width": r.randint(20, 100)}
    if k == "panel":
        return {"kind": k, "content": "\n".join(text(r) for _ in range(r.randint(1, 3))), "title": text(r) if r.random() < 0.6 else "",
                "title_align": r.choice(["left", "center", "right"]), "border_style": r.choice(STYLES),
                "fit": r.random() < 0.4, "padding": r.choice([[0, 1], [1, 2], [0, 0]]), "width": r.randint(10, 100)}
    if k == "tree":
        def node(depth):
            d = {"label": text(r), "children": []}
            if depth < 3:
                for _ in range(r.randint(0, 3)):
                    d["children"].append(node(depth + 1))
            return d
        return {"kind": k, "root": node(0), "guide_style": r.choice(STYLES + ["grey50", "bold"]), "width": r.randint(15, 80)}
    if k == "padding":
        return {"kind": k, "content": text(r), "pad": r.choice([[1], [0, 1], [1, 2, 0, 3]]), "style": r.choice(STYLES), "width": r.randint(10, 60)}
    return {"kind": "columns", "items": [text(r) for _ in range(r.randint(1, 8))], "width": r.randint(10, 80)}


def build(c):
    if c["kind"] == "table":
        t = Table(title=c["title"] or None, box=getattr(rbox, c["box"]) if c["box"] else None, show_lines=c["show_lines"],
                  show_header=c["show_header"], show_edge=c["show_edge"], expand=c["expand"], header_style=c["header_style"],
                  border_style=c["border_style"] or None, padding=tuple(c["padding"]), pad_edge=c["pad_edge"], highlight=c["highlight"])
        for col in c["columns"]:
            t.add_column(col["header"], style=col["style"] or None, justify=col["justify"], no_wrap=col["no_wrap"],
                         width=col.get("width"), min_width=col.get("min_width"), ratio=col.get("ratio"))
        for i, row in enumerate(c["rows"]):
            t.add_row(*row, style=(c["row_style"] or None) if i % 2 else None)
        return t
    if c["kind"] == "panel":
        return Panel(c["content"], title=c["title"] or None, title_align=c["title_align"], border_style=c["border_style"] or "none",
                     expand=not c["fit"], padding=tuple(c["padding"]))
    if c["kind"] == "tree":
        def mk(d, parent=None):
            t = Tree(d["label"], guide_style=c["guide_style"] or "tree.line") if parent is None else parent.add(d["label"])
            for ch in d["children"]:
                mk(ch, t)
            return t
        return mk(c["root"])
    if c["kind"] == "padding":
        return Padding(c["content"], tuple(c["pad"]), style=c["style"] or "none")
    return Columns(c["items"])


def main():
    out, n, seed = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
    systems = sys.argv[4:] or ["standard", "256", "truecolor", "none"]
    r = random.Random(seed)
    rows = []
    for _ in range(n):
        c = gen(r)
        res = {}
        for s in systems:
            buf = io.StringIO()
            con = Console(file=buf, force_terminal=s != "none", color_system=None if s == "none" else s, width=c["width"])
            try:
                con.print(build(c))
                res[s] = buf.getvalue()
            except Exception as e:  # noqa: BLE001
                res[s] = "ERR " + type(e).__name__ + ": " + str(e)
        rows.append({"case": c, "out": res})
    json.dump(rows, open(out, "w"), ensure_ascii=False)


main()
