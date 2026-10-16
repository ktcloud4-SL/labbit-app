"""Run exact generated owned-route functions against a harmless in-memory kernel."""
import ast
import contextlib
import json
import pathlib
import subprocess
import sys

scripts = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
tree = ast.parse(scripts["route-ab"])
functions = [n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name in {"owned_route_entries", "owned_route"}]
assert len(functions) == 2
unit = compile(ast.Module(body=functions, type_ignores=[]), "generated-owned-route.py", "exec")
rows = []
for mode in ["normal", "add-timeout-applied", "add-timeout-not-applied", "add-rejected", "body-failed", "preexisting", "lookup-failed-before", "lookup-failed-after", "delete-failed"]:
    state = {"owned": mode == "preexisting", "adds": 0, "deletes": 0, "queries": 0}
    route = {"dst": "1.1.1.1", "gateway": "192.0.2.1", "dev": "lab0", "metric": 50}

    def bounded_output(args, **kwargs):
        state["queries"] += 1
        if mode == "lookup-failed-before" or (mode == "lookup-failed-after" and state["queries"] >= 2):
            raise subprocess.CalledProcessError(1, args)
        return json.dumps([route] if state["owned"] else []).encode()

    def bounded_run(args, **kwargs):
        if "add" in args:
            state["adds"] += 1
            if mode == "add-rejected": raise subprocess.CalledProcessError(1, args)
            state["owned"] = mode != "add-timeout-not-applied"
            if mode.startswith("add-timeout"): raise subprocess.TimeoutExpired(args, 8)
        elif "del" in args:
            state["deletes"] += 1
            if mode == "delete-failed": raise subprocess.CalledProcessError(1, args)
            state["owned"] = False
        else:
            raise AssertionError(args)
        return subprocess.CompletedProcess(args, 0)

    env = dict(contextmanager=contextlib.contextmanager, json=json, subprocess=subprocess,
               bounded_output=bounded_output, bounded_run=bounded_run)
    exec(unit, env)
    observed = None
    try:
        with env["owned_route"](["1.1.1.1/32", "via", "192.0.2.1", "dev", "lab0", "metric", "50"]):
            if mode == "body-failed": raise RuntimeError("original body failure")
    except Exception as exc:
        observed = type(exc).__name__
    expected = {
        "normal": (None, False, 1, 1),
        "add-timeout-applied": ("TimeoutExpired", False, 1, 1),
        "add-timeout-not-applied": ("TimeoutExpired", False, 1, 0),
        "add-rejected": ("CalledProcessError", False, 1, 0),
        "body-failed": ("RuntimeError", False, 1, 1),
        "preexisting": ("AssertionError", True, 0, 0),
        "lookup-failed-before": ("CalledProcessError", False, 0, 0),
        "lookup-failed-after": ("CalledProcessError", True, 1, 0),
        "delete-failed": ("CalledProcessError", True, 1, 1),
    }[mode]
    actual = (observed, state["owned"], state["adds"], state["deletes"])
    rows.append({"mode": mode, "exception": observed, **state, "passed": actual == expected})

# The full acceptance helper omits Apt routing; the explicitly separate native
# helper may use the same owned-route context for compiler installation only.
apt_tree = ast.parse(scripts["apt"])
apt_add_calls = [node for node in ast.walk(apt_tree) if isinstance(node, ast.Call)
                 and isinstance(node.func, ast.Name) and node.func.id == "bounded_run"
                 and node.args and isinstance(node.args[0], ast.List)
                 and any(isinstance(x, ast.Constant) and x.value == "add" for x in node.args[0].elts)]
apt_owned_contexts = [node for node in ast.walk(apt_tree) if isinstance(node, ast.With)
                      and any(isinstance(item.context_expr, ast.Call)
                              and isinstance(item.context_expr.func, ast.Name)
                              and item.context_expr.func.id == "owned_route" for item in node.items)]
expected_contexts = 1 if "--apt-owned-route" in sys.argv[3:] else 0
result = {"real_network_or_process_operations": 0, "cases": rows, "apt_direct_route_add_calls": len(apt_add_calls),
          "apt_owned_route_contexts": len(apt_owned_contexts), "expected_apt_owned_route_contexts": expected_contexts,
          "passed": all(x["passed"] for x in rows) and not apt_add_calls and len(apt_owned_contexts) == expected_contexts}
pathlib.Path(sys.argv[2]).write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
print(json.dumps(result))
sys.exit(0 if result["passed"] else 1)
