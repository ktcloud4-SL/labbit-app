import json
import pathlib

root = pathlib.Path("C:/Users/user/Documents/ChatGPT/3차 2")
run = root / "tmp/lbt174-routing-fix-20261016"
out = root / "tmp/lbt174-routing-fix-independent-20261016"
identity = json.loads((out / "source-current-identity.json").read_text(encoding="utf-8"))
rows = []
for name in ["go-test", "go-race"]:
    meta = json.loads((run / (name + "-result.json")).read_text(encoding="utf-8-sig"))
    events = [json.loads(line) for line in (run / (name + ".log")).read_text(encoding="utf-8-sig").splitlines() if line.strip()]
    tests = [e for e in events if e.get("Test")]
    row = {"name": name, "exitCode": meta["exitCode"], "sourceSHA256": meta["sourceSHA256"],
           "sourceMatchesCurrentGoModule": meta["sourceSHA256"] == identity["goModuleSHA256"],
           "sourceUnchangedAsRecordedByRunner": meta["sourceUnchanged"],
           "testPassEvents": sum(e["Action"] == "pass" for e in tests),
           "testSkip": [e["Test"] for e in tests if e["Action"] == "skip"],
           "testFail": [e["Test"] for e in tests if e["Action"] == "fail"],
           "packagePass": sorted({e["Package"] for e in events if not e.get("Test") and e["Action"] == "pass"}),
           "packageFail": [e["Package"] for e in events if not e.get("Test") and e["Action"] == "fail"],
           "raceWarningLines": [e.get("Output", "") for e in events if "WARNING: DATA RACE" in e.get("Output", "")]}
    rows.append(row)
result = {"executedByReviewer": False, "rawJSONLReadByReviewer": True, "results": rows}
(out / "current-raw-results.json").write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
print(json.dumps([{**r, "packagePass": len(r["packagePass"]), "skipCount": len(r["testSkip"])} for r in rows]))
assert all(r["exitCode"] == 0 and r["sourceMatchesCurrentGoModule"] and not r["testFail"] and not r["packageFail"] and not r["raceWarningLines"] for r in rows)
