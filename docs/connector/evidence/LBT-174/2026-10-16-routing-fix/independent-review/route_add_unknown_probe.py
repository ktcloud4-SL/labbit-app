"""No network/process operations: inject timeout-after-apply into exact generated code."""
import json
import pathlib
import subprocess
import sys

scripts = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
rows = []
for name, start in [("route-ab", "\ndefaults=json.loads"), ("intentional-timeout", "\ntarget='1.0.0.1/32'"), ("apt", "\ndefaults=json.loads")]:
    script = scripts[name]
    body = script[script.index(start) + 1:]
    state = {"owned": False, "delete_calls": 0, "add_calls": 0}

    def bounded_output(args, **kwargs):
        if args[:5] == ["ip", "-j", "route", "show", "exact"]:
            return json.dumps([{"dst": args[5]}] if state["owned"] else []).encode()
        if args[:5] == ["ip", "-j", "route", "show", "default"]:
            return json.dumps([{"dev": "lab0", "gateway": "192.0.2.1", "metric": 100}]).encode()
        if args == ["ip", "-j", "link"]:
            return json.dumps([{"ifname": "management0"}, {"ifname": "lab0"}]).encode()
        raise AssertionError(args)

    def bounded_run(args, **kwargs):
        if "add" in args:
            state["add_calls"] += 1
            state["owned"] = True
            raise subprocess.TimeoutExpired(args, 8)
        if "del" in args:
            state["delete_calls"] += 1
            state["owned"] = False
            return subprocess.CompletedProcess(args, 0)
        raise AssertionError(args)

    env = dict(json=json, subprocess=subprocess, bounded_output=bounded_output,
               bounded_run=bounded_run, role=lambda dev: "management" if dev == "management0" else "lab",
               route=lambda: [{"role": "lab", "hasGateway": True}], probe=lambda: {"connectCode": 0},
               managementGateway="192.0.2.254")
    try:
        exec(compile(body, name + "-generated.py", "exec"), env)
    except subprocess.TimeoutExpired:
        pass
    rows.append({"script": name, **state, "passed_cleanup": not state["owned"], "real_operations": 0})
result = {"fault": "route-add accepted then local subprocess timeout before reply", "cases": rows}
pathlib.Path(sys.argv[2]).write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
print(json.dumps(result))
sys.exit(0 if all(row["passed_cleanup"] for row in rows) else 1)
