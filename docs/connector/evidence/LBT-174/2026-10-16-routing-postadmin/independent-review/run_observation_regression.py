"""Loopback-only before/after observation fault probes; no cloud calls."""
from pathlib import Path
from datetime import datetime, timezone
import hashlib
import json
import subprocess
import time

OUT = Path(__file__).resolve().parent
REPO = OUT.parent / "labbit-connector-gates-20261015"
GO = Path(r"C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\go.exe")
PARENT = OUT.parent / "lbt174-routing-postadmin-20261016"
MANIFEST = PARENT / "final-verified/source-manifest.json"


def source_identity():
    manifest = json.loads(MANIFEST.read_text(encoding="utf8"))
    actual = {path: hashlib.sha256((REPO / path).read_bytes().replace(b"\r\n", b"\n")).hexdigest() for path in manifest["files"]}
    return {"count": len(actual), "aggregate": hashlib.sha256(json.dumps(actual, sort_keys=True).encode()).hexdigest(), "mismatches": [path for path in actual if actual[path] != manifest["files"][path]]}


before = source_identity()
assert not before["mismatches"]
records = []
for stage in ("restored-before", "after"):
    replace = {str(REPO / "internal/connector/provider/openstack/independent_management_observation_boundary_test.go"): str(OUT / "management_observation_boundary_test.go")}
    if stage == "restored-before":
        for name in ("topology.go", "management_network.go"):
            replace[str(REPO / "internal/connector/provider/openstack" / name)] = str(OUT / (name + ".before-observation-fix.txt"))
    overlay = OUT / ("management-observation-boundary-" + stage + "-overlay.json")
    overlay.write_text(json.dumps({"Replace": replace}, indent=2) + "\n", encoding="utf8")
    args = [str(GO), "test", "-overlay", str(overlay), "-json", "-run", "^TestIndependentManagementObservationBoundary$", "-count=1", "-timeout=30s", "./internal/connector/provider/openstack"]
    started = time.monotonic()
    result = subprocess.run(args, cwd=REPO, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=90)
    log = OUT / ("management-observation-boundary-" + stage + ".log")
    log.write_bytes(result.stdout)
    events = []
    for line in result.stdout.decode("utf8", errors="replace").splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if event.get("Action") in ("pass", "fail", "skip"):
            events.append({key: event[key] for key in ("Action", "Test", "Package") if key in event})
    records.append({"stage": stage, "command": args, "exitCode": result.returncode, "elapsedSeconds": round(time.monotonic() - started, 3), "logSHA256": hashlib.sha256(result.stdout).hexdigest(), "overlayInputs": {str(Path(path).name): hashlib.sha256(Path(path).read_bytes()).hexdigest() for path in replace.values()}, "events": events})
    if stage == "restored-before":
        assert result.returncode != 0
        assert any(event.get("Test", "").endswith("/stable") and event["Action"] == "pass" for event in events)
        assert sum(event["Action"] == "fail" and "/" in event.get("Test", "") for event in events) == 5
    else:
        assert result.returncode == 0
        assert sum(event["Action"] == "pass" and "/" in event.get("Test", "") for event in events) == 6
after = source_identity()
assert before == after
report = {"recordedUTC": datetime.now(timezone.utc).isoformat(), "sourceBefore": before, "sourceAfter": after, "manifest": str(MANIFEST), "mode": "local httptest fault only; restored-before overlays two saved pre-fix production files on final tests; other final tests unchanged", "runs": records}
(OUT / "management-observation-regression.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf8")
print(json.dumps({"source": before, "stages": [{"stage": record["stage"], "exitCode": record["exitCode"]} for record in records]}))
