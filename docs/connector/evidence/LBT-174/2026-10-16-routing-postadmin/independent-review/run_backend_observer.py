"""Explicitly authorized agent GET allowlist observer; no cloud mutation."""
from pathlib import Path
from datetime import datetime, timezone
import hashlib
import json
import os
import subprocess
import time

OUT = Path(__file__).resolve().parent
ROOT = OUT.parent / "labbit-connector-gates-20261015"
GO = Path(r"C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\go.exe")
source_manifest = OUT.parent / "lbt174-routing-postadmin-20261016/source-manifest.json"
expected = json.loads(source_manifest.read_text(encoding="utf8"))["files"]

def identity():
    actual = {path: hashlib.sha256((ROOT / path).read_bytes().replace(b"\r\n", b"\n")).hexdigest() for path in expected}
    return {"files": len(actual), "sourceSHA256": hashlib.sha256(json.dumps(actual, sort_keys=True).encode()).hexdigest(), "mismatches": sum(actual[path] != expected[path] for path in expected)}

overlay = {"Replace": {str(ROOT / "internal/connector/provider/openstack/local_independent_backend_config_test.go"): str(OUT / "backend_configuration_observer_test.go")}}
(OUT / "backend-configuration-overlay.json").write_text(json.dumps(overlay, indent=2) + "\n", encoding="utf8")
before = identity()
assert before["mismatches"] == 0
args = [str(GO), "test", "-overlay", str(OUT / "backend-configuration-overlay.json"), "-json", "-run", "^TestIndependentReadOnlyDHCPBackendConfiguration$", "-count=1", "-timeout=45s", "./internal/connector/provider/openstack"]
env = os.environ.copy()
env.update({"LABBIT_PROVIDER_CONFIG_FILE": r"C:\Users\user\.config\openstack\clouds.yaml", "OS_CLOUD": "labbit-test"})
start = time.monotonic()
metadata = {"startedUTC": datetime.now(timezone.utc).isoformat(), "sourceBefore": before, "mode": "GET /agents typed allowlist only; initialization authentication; no mutation", "command": args, "testSHA256": hashlib.sha256((OUT / "backend_configuration_observer_test.go").read_bytes()).hexdigest()}
try:
    with (OUT / "backend-configuration-observer.log").open("wb") as log:
        proc = subprocess.run(args, cwd=ROOT, env=env, stdout=log, stderr=subprocess.STDOUT, timeout=60)
    metadata["exitCode"] = proc.returncode
except BaseException as exc:
    metadata["runnerExceptionType"] = type(exc).__name__
    raise
finally:
    metadata.update({"sourceAfter": identity(), "finishedUTC": datetime.now(timezone.utc).isoformat(), "elapsedSeconds": round(time.monotonic() - start, 3)})
    (OUT / "backend-configuration-run.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + "\n", encoding="utf8")
    print(json.dumps({key: metadata[key] for key in ("sourceBefore", "sourceAfter", "elapsedSeconds", "exitCode") if key in metadata}))
assert metadata["exitCode"] == 0
