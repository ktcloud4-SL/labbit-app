"""Parse exact decoded Go-generator output. Does not execute remote commands."""
import ast
import hashlib
import json
import pathlib
import sys

dump_path = pathlib.Path(sys.argv[1])
result_path = pathlib.Path(sys.argv[2])
scripts = json.loads(dump_path.read_text(encoding="utf-8"))
expected = {"reachability", "route-ab", "apt", "intentional-timeout", "renew-lab-first", "renew-management-first"}
assert set(scripts) == expected, set(scripts)
results = []
for name, script in sorted(scripts.items()):
    row = {"name": name, "sha256": hashlib.sha256(script.encode()).hexdigest(), "bytes": len(script.encode())}
    try:
        tree = ast.parse(script, filename=name + ".py", mode="exec")
        row.update(passed=True, statements=len(tree.body))
    except SyntaxError as exc:
        row.update(passed=False, error=type(exc).__name__, line=exc.lineno, reason=exc.msg)
    results.append(row)
result = {"dump_sha256": hashlib.sha256(dump_path.read_bytes()).hexdigest(), "executed": False, "scripts": results, "passed": all(row["passed"] for row in results)}
result_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(json.dumps(result, ensure_ascii=False))
sys.exit(0 if result["passed"] else 1)
