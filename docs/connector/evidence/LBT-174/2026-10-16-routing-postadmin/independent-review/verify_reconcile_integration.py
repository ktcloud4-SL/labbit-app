"""Read-only newer-main local integration raw/source reconciliation."""
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import subprocess

OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/"lbt174-routing-postadmin-20261016"
STAGE=ROOT/"latest-main-reconcile-check"
TREE=ROOT/"latest-main-reconcile-integration"
PRIMARY=OUT.parent/"labbit-connector-gates-20261015"
def read(p): return json.loads(p.read_text(encoding="utf-8-sig"))
def sha(p): return hashlib.sha256(p.read_bytes()).hexdigest()
def lfsha(p): return hashlib.sha256(p.read_bytes().replace(b"\r\n",b"\n")).hexdigest()
def git(*args,cwd=TREE): return subprocess.check_output(["git",*args],cwd=cwd)
manifest=read(STAGE/"source-manifest.json")
errors=[p for p,h in manifest["files"].items() if lfsha(TREE/p)!=h]
assert len(manifest["files"])==535 and not errors
assert git("rev-parse","HEAD").decode().strip()=="1e2b9c20fa2cfd7d7d449c8abba8558b5a614df4"
scope=read(OUT/"source-scope-index.json")
rows=scope.get("files",scope.get("paths",[]))
paths=list(rows) if isinstance(rows,dict) else [r.get("path",r.get("file")) for r in rows]
go_paths=[p for p in paths if p.endswith(".go")]
branch_errors=[p for p in go_paths if lfsha(TREE/p)!=lfsha(PRIMARY/p)]
assert len(go_paths)==36 and not branch_errors
checks=[]
for path in sorted(STAGE.glob("*-result.json")):
    r=read(path)
    if "sourceSHA256" not in r or "exitCode" not in r: continue
    assert r["exitCode"]==0 and r.get("sourceUnchanged") is True,path.name
    checks.append({"file":path.name,"exitCode":r["exitCode"],"sourceUnchanged":True,"sourceSHA256":r["sourceSHA256"],"resultSHA256":sha(path)})
results={}
for name in ("go-test","postgres-integration"):
    path=STAGE/(name+".log")
    events=[json.loads(line) for line in path.read_text(encoding="utf-8-sig").splitlines() if line.startswith("{")]
    tests=Counter(e["Action"] for e in events if e.get("Test"))
    packages=Counter(e["Action"] for e in events if not e.get("Test"))
    assert tests["fail"]==packages["fail"]==0
    assert packages["pass"]==(43 if name=="go-test" else 5)
    results[name]={"testPassEvents":tests["pass"],"testSkipEvents":tests["skip"],"testFailEvents":tests["fail"],"packagePassEvents":packages["pass"],"sha256":sha(path),
                   "skippedTests":[{"package":e["Package"],"test":e["Test"]} for e in events if e.get("Test") and e["Action"]=="skip"]}
sql_paths=git("ls-files","db/migrations/*.sql").decode().splitlines()
sql=[]
for p in sql_paths:
    data=(TREE/p).read_bytes()
    head=git("show","HEAD:"+p)
    assert data.replace(b"\r\n",b"\n")==head.replace(b"\r\n",b"\n"),p
    sql.append({"path":p,"workingRawSHA256":hashlib.sha256(data).hexdigest(),"canonicalSHA256":lfsha(TREE/p),"HEADCanonicalSHA256":hashlib.sha256(head.replace(b"\r\n",b"\n")).hexdigest()})
assert len(sql)==9
assert not git("diff","HEAD","--name-only","--","db/migrations").strip()
sql_initial=read(STAGE/"migration-source-identity.json")
sql_final=read(STAGE/"migration-source-final-identity.json")
assert sql_initial["files"]==sql_final["files"]
assert all(r["rawIdenticalOrLFtoCRLFOnly"] for r in sql_final["files"])
assert not sql_initial["workingDiff"] and not sql_final["workingDiff"]
assert {r["path"]:r["workingRawSHA256"] for r in sql_final["files"]}=={r["path"]:r["workingRawSHA256"] for r in sql}
web=read(ROOT/"latest-main-check/web-source-identity.json")
web_errors=[p for p,h in web["files"].items() if lfsha(TREE/p)!=h]
assert len(web["files"])==66 and not web_errors
assert git("rev-parse","HEAD:web").decode().strip()==web["headWebTree"]
db_owner=read(STAGE/"database-owner-result.json")
assert db_owner["exitCode"]==db_owner["queryExit"]==db_owner["stopExit"]==0 and db_owner["sourceUnchanged"] is True
assert (STAGE/"postgres-cleanup.log").read_text(encoding="utf-8-sig").strip()=="0"
report={"observedUTC":datetime.now(timezone.utc).isoformat(),"head":"1e2b9c20fa2cfd7d7d449c8abba8558b5a614df4",
        "sourceFiles":535,"sourceSHA256":manifest["sourceSHA256"],"sourceMismatch":errors,
        "branchChangedGoFiles":36,"branchChangedGoMismatches":branch_errors,"checks":checks,"rawResults":results,
        "databaseOwnership":{"exitCode":0,"queryExit":0,"stopExit":0,"temporaryDBs":0,"sourceUnchanged":True,"metadataSHA256":sha(STAGE/"database-owner-result.json")},
        "sqlMigrations":sql,"sqlWorkingChanges":[],"sqlScope":"SQL is outside535 Go/module/contracts/runtime source manifest; separately matches pinned HEAD9 migrations at review. Root during-run and post-run SQL9 raw/canonical hashes match. No claim runner535 hash included SQL or SQL initial snapshot preceded DB start.",
        "sqlObservationTimes":{"firstUTC":sql_initial["observedUTC"],"finalUTC":sql_final["observedUTC"],"firstPhase":sql_initial["phase"],"finalPhase":sql_final["phase"]},
        "webBridge":{"files":66,"mismatches":web_errors,"sameTrackedTree":web["headWebTree"],"samePackageLock":lfsha(TREE/"web/package-lock.json")==web["files"]["web/package-lock.json"],"priorExecuted":"latest-main-check: typecheck/lint/16files217tests/build PASS"},
        "primaryHEAD":git("rev-parse","HEAD",cwd=PRIMARY).decode().strip(),"primaryStagedFiles":git("diff","--cached","--name-only",cwd=PRIMARY).decode().splitlines(),
        "limits":["Synthetic local integration checkout, not a primary branch rebase or remote CI.","Real PostgreSQL tests and Mock Provider/control fixture are distinct from live SaaS/Browser C3."]}
assert report["primaryHEAD"]=="0051d8d6803fd447f7b690dd895a0a928c3c7302" and not report["primaryStagedFiles"]
(OUT/"latest-main-reconcile-verification.json").write_text(json.dumps(report,indent=2)+"\n",encoding="utf-8")
print(json.dumps({"source":535,"mismatch":0,"branchGo":36,"sql":9,"web":66,"results":results}))
