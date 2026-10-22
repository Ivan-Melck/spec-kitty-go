"""Reference dumper for pyast tests: prints, per file, the facts the Go
parser must reproduce (stdlib ast): definitions, string constants and the
assertion-bearing nodes in ast.walk order with their names/constants."""
import ast
import json
import sys


def bearing(node):
    if isinstance(node, ast.Assert):
        return True
    if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute) and node.func.attr.startswith("assert"):
        return True
    return False


def names(node):
    out = set()
    for c in ast.walk(node):
        if isinstance(c, ast.Name):
            out.add(c.id)
        elif isinstance(c, ast.Attribute):
            out.add(c.attr)
    return sorted(out)


def consts(node):
    return sorted({c.value for c in ast.walk(node) if isinstance(c, ast.Constant) and isinstance(c.value, str)})


def module_exports(tree):
    out = []
    for node in tree.body:
        if isinstance(node, ast.Import):
            out += ["import:" + (a.asname or a.name.split(".")[0]) for a in node.names]
        elif isinstance(node, ast.ImportFrom):
            out += ["from:" + a.name + ":" + (a.asname or "") for a in node.names]
        elif isinstance(node, ast.Assign) and any(isinstance(t, ast.Name) and t.id == "__all__" for t in node.targets):
            if isinstance(node.value, (ast.List, ast.Tuple, ast.Set)):
                out += ["all:" + e.value for e in node.value.elts if isinstance(e, ast.Constant) and isinstance(e.value, str)]
    return out


result = {}
for path in sys.argv[1:]:
    try:
        src = open(path, encoding="utf-8", errors="replace").read()
        tree = ast.parse(src)
    except SyntaxError:
        result[path] = None
        continue
    defs = [[n.name, n.lineno] for n in ast.walk(tree) if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef))]
    strs = sorted({(n.value, n.lineno) for n in ast.walk(tree) if isinstance(n, ast.Constant) and isinstance(n.value, str)})
    asserts = [[type(n).__name__, n.lineno, names(n), consts(n)] for n in ast.walk(tree) if bearing(n)]
    result[path] = {"defs": defs or [], "strs": [list(s) for s in strs], "asserts": asserts, "exports": module_exports(tree)}
json.dump(result, sys.stdout)
