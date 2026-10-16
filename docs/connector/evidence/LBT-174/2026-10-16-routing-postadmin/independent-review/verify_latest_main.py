"""Read-only independent reconciliation of local synthetic-main validation."""
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import subprocess

OUT = Path(__file__).resolve().parent
ROOT = OUT.parent / "lbt174-routing-postadmin-20261016"
STAGE = ROOT / "latest-main-check"
TREE = ROOT / "latest-main-integration"
PRIMARY = OUT.parent / "labbit-connector-gates-20261015"

def js(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def lfsha(path):
    return hashlib.sha256(path.read_bytes().replace(b"\r\n", b"\n")).hexdigest()

def git(*args, cwd=TREE):
    return subprocess.check_output(["git", *args], cwd=cwd).decode("utf-8").strip()

manifest = js(STAGE / "source-manifest.json")
mismatches = [p for p, h in manifest["files"].items() if lfsha(TREE / p) != h]
assert not mismatches
scope = js(OUT / "source-scope-index.json")
rows = scope.get("files", scope.get("paths", []))
if isinstance(rows, dict):
    go_paths = [p for p in rows if p.endswith(".go")]
else:
    go_paths = [r.get("path", r.get("file")) for r in rows if r.get("path", r.get("file", "")).endswith(".go")]
assert len(go_paths) == 36, len(go_paths)
branch_mismatches = [p for p in go_paths if lfsha(TREE / p) != lfsha(PRIMARY / p)]
assert not branch_mismatches

checks = []
for path in sorted(STAGE.glob("*-result.json")):
    record = js(path)
    if "exitCode" not in record:
        continue
    assert record["exitCode"] == 0, path.name
    assert record.get("sourceUnchanged") is True, path.name
    checks.append({"name": record["name"], "exitCode": record["exitCode"],
                   "sourceUnchanged": record["sourceUnchanged"],
                   "sourceSHA256": record["sourceSHA256"],
                   "resultSHA256": sha(path)})

logs = {}
for name in ("go-test", "postgres-integration"):
    path = STAGE / f"{name}.log"
    events = [json.loads(line) for line in path.read_text(encoding="utf-8-sig").splitlines() if line.startswith("{")]
    tests = Counter(e["Action"] for e in events if e.get("Test"))
    packages = Counter(e["Action"] for e in events if not e.get("Test"))
    assert tests["fail"] == packages["fail"] == 0
    logs[name] = {"testPassEvents": tests["pass"], "testSkipEvents": tests["skip"],
                  "testFailEvents": tests["fail"], "packagePassEvents": packages["pass"],
                  "packageFailEvents": packages["fail"], "sha256": sha(path),
                  "skippedTests": [{"package": e["Package"], "test": e["Test"]} for e in events if e.get("Test") and e["Action"] == "skip"]}

web_identity = js(STAGE / "web-source-identity.json")
web_mismatches = [p for p,h in web_identity["files"].items() if lfsha(TREE / p) != h]
assert not web_mismatches and not web_identity["headMismatches"]
assert web_identity["trackedWebChanges"] == ""
web_text = (STAGE / "web-test.log").read_text(encoding="utf-8-sig")
assert re.search(r"Test Files\s+16 passed \(16\)", web_text)
assert re.search(r"Tests\s+217 passed \(217\)", web_text)

old = js(OUT / "latest-main-integration-check.json")
old.update({"recordedUTC": datetime.now(timezone.utc).isoformat(),
            "worktreeHEAD": git("rev-parse", "HEAD"),
            "primaryHEAD": git("rev-parse", "HEAD", cwd=PRIMARY),
            "primaryStagedFiles": git("diff", "--cached", "--name-only", cwd=PRIMARY).splitlines(),
            "sourceFiles": len(manifest["files"]), "sourceMismatches": mismatches,
            "branchChangedGoFileCount": len(go_paths), "branchChangedGoMismatches": branch_mismatches,
            "completedChecks": checks, "rawResults": logs,
            "postgres": "real local PostgreSQL, 5 package command exit0, 1415 pass events, 1 explicitly optional skip; not a full Browser/SaaS E2E",
            "web": {"trackedFiles": len(web_identity["files"]), "mismatches": web_mismatches,
                    "headTree": web_identity["headWebTree"], "testFiles": 16, "testPass": 217,
                    "packageLockMatchesTrackedHEAD": git("hash-object", "web/package-lock.json") == git("rev-parse", "HEAD:web/package-lock.json")}})
assert logs["postgres-integration"]["testPassEvents"] == 1415
assert logs["postgres-integration"]["testSkipEvents"] == 1
(OUT / "latest-main-integration-check.json").write_text(json.dumps(old, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
print(json.dumps({"files": len(manifest["files"]), "branchGo": len(go_paths), "sourceMismatch": len(mismatches),
                  "checks": len(checks), "go": logs["go-test"], "postgres": logs["postgres-integration"], "web": old["web"]}, ensure_ascii=False))
