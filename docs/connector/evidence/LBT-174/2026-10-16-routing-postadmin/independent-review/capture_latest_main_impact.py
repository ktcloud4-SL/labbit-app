"""Capture already-fetched newest-main source impact without modifying refs."""
import hashlib
import json
from pathlib import Path
import subprocess

OUT=Path(__file__).resolve().parent
REPO=OUT.parent/"labbit-connector-gates-20261015"
BEFORE="ee05485bd9a1668f222e7a54fb5f880ea197e35a"
AFTER="1e2b9c20fa2cfd7d7d449c8abba8558b5a614df4"
def git(*args): return subprocess.check_output(["git",*args],cwd=REPO)
paths=git("diff","--name-only",BEFORE,AFTER).decode().splitlines()
boundary_paths=["internal/connector","internal/server/preview","internal/server/realtime","internal/server/connector","contracts/http","contracts/realtime","contracts/connector","go.mod","go.sum","web"]
boundary_changes=git("diff","--name-only",BEFORE,AFTER,"--",*boundary_paths).decode().splitlines()
assert not boundary_changes
production_paths=[p for p in paths if p.endswith(".go") and not p.endswith("_test.go")]
context_paths=production_paths+["runtime/contract.yaml","db/migrations/README.md","docs/backend/README.md"]
diff=git("diff",BEFORE,AFTER,"--",*context_paths)
(OUT/"latest-main-1e2-production-diff.patch").write_bytes(diff)
report={"previousReviewedIntegration":BEFORE,"newMain":AFTER,"commitSubject":git("show","-s","--format=%s",AFTER).decode().strip(),
        "changedPaths":paths,"productionPaths":production_paths,"boundaryPathsCompared":boundary_paths,"boundaryChanges":boundary_changes,
        "newSQLMigrations": [p for p in paths if p.startswith("db/migrations/") and p.endswith(".sql")],
        "productionDiffSHA256":hashlib.sha256(diff).hexdigest(),
        "runtimeDelta":"Read-only UNKNOWN reconciliation claim/lease fencing; only CLEANUP complete exact known absence may finish; no mutation replay or PROVISION/RESET READY inference",
        "impact":"Existing tested branch Preview/Provider/Connector component sources and HTTP/WSS consumers are unchanged. New production worker/store wiring requires a distinct integrated-main verification; ee05485 PASS cannot establish this new path.",
        "integrationStatus":"Subsequently completed: see latest-main-reconcile-verification.json for separate1e2 source535 Go/vet/build/realPG5package verification and source/SQL/Web bridge.",
        "reviewerBoundary":"Read-only diff/source impact review; no claim of full independent audit of unrelated LBT179, no primary checkout/ref/product mutation"}
(OUT/"latest-main-1e2-impact.json").write_text(json.dumps(report,indent=2,ensure_ascii=False)+"\n",encoding="utf-8")
print(json.dumps({"paths":len(paths),"productionGo":len(production_paths),"relatedBoundaryChanges":boundary_changes,"newSQLMigrations":report["newSQLMigrations"]}))
