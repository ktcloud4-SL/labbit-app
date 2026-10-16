"""Compile/call only script generators; never run diagnostic/cloud test."""
import ast
import hashlib
import json
import os
import pathlib
import subprocess

OUT = pathlib.Path(__file__).resolve().parent
RUN = OUT.parent / "lbt174-routing-fix-20261016"
REPO = OUT.parent / "labbit-connector-gates-20261015"
GO = pathlib.Path("C:/Users/user/AppData/Local/Programs/Go-1.27.1/go/bin/go.exe")
results = []
for mode, helper in [("full-lifecycle", RUN / "local_probe_test.go"), ("native-independent", RUN / "native-independent/local_probe_test.go")]:
    overlay = OUT / f"latest-{mode}-generator-overlay.json"
    dump = OUT / f"latest-{mode}-generated-python.json"
    overlay.write_text(json.dumps({"Replace": {
        str(REPO / "internal/connector/provider/openstack/local_network_race_probe_test.go"): str(helper),
        str(REPO / "internal/connector/provider/openstack/review_dump_remote_test.go"): str(OUT / "remote_python_dump_test.go"),
    }}, indent=2) + "\n", encoding="utf-8")
    env = os.environ.copy()
    env["LBT174_REVIEW_PYTHON_DUMP"] = str(dump)
    # Anchored name cannot run the opt-in cloud fixture, even if an ambient env is set.
    command = [str(GO), "test", "-overlay=" + str(overlay), "-run=^TestReviewDumpGeneratedRemotePython$", "-count=1", "-v", "./internal/connector/provider/openstack"]
    proc = subprocess.run(command, cwd=REPO, env=env, capture_output=True, timeout=90)
    (OUT / f"latest-{mode}-generator.log").write_bytes(proc.stdout + proc.stderr)
    row = {"mode": mode, "helperSHA256": hashlib.sha256(helper.read_bytes()).hexdigest(), "command": command, "exitCode": proc.returncode, "remotePythonExecuted": False, "cloudOperations": 0, "scripts": []}
    assert proc.returncode == 0, proc.stderr.decode(errors="replace")
    scripts = json.loads(dump.read_text(encoding="utf-8"))
    for name, script in sorted(scripts.items()):
        tree = ast.parse(script, filename=mode + "-" + name + ".py")
        row["scripts"].append({"name": name, "sha256": hashlib.sha256(script.encode()).hexdigest(), "bytes": len(script.encode()), "astParsePassed": True, "statements": len(tree.body)})
    assert len(row["scripts"]) == 6
    results.append(row)
(OUT / "latest-generator-check.json").write_text(json.dumps(results, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(json.dumps([{"mode": row["mode"], "exitCode": row["exitCode"], "scriptCount": len(row["scripts"]), "cloudOperations": 0} for row in results]))
