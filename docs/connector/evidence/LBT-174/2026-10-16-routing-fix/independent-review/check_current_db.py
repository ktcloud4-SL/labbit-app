import json
import pathlib

OUT = pathlib.Path(__file__).resolve().parent
RUN = OUT.parent / "lbt174-routing-fix-20261016"
source = json.loads((OUT / "source-current-identity.json").read_text())
meta = json.loads((RUN / "postgres-integration-result.json").read_text(encoding="utf-8-sig"))
vet = json.loads((RUN / "postgres-vet-result.json").read_text(encoding="utf-8-sig"))
events = [json.loads(line) for line in (RUN / "postgres-integration.log").read_text(encoding="utf-8-sig").splitlines() if line.strip()]
run = json.loads((RUN / "db-run-result.json").read_text(encoding="utf-8-sig"))
result = {
    "sourceSHA256": meta["sourceSHA256"],
    "sourceMatchesCurrentGoModule": meta["sourceSHA256"] == source["goModuleSHA256"],
    "sourceUnchangedAsRecordedByRunner": meta["sourceUnchanged"],
    "executedByReviewer": False, "rawJSONLReadByReviewer": True,
    "exitCode": meta["exitCode"], "vetExitCode": vet["exitCode"],
    "testPassEvents": sum(e.get("Test") is not None and e["Action"] == "pass" for e in events),
    "testSkip": [e["Test"] for e in events if e.get("Test") and e["Action"] == "skip"],
    "testFail": [e["Test"] for e in events if e.get("Test") and e["Action"] == "fail"],
    "packagePass": sorted({e["Package"] for e in events if not e.get("Test") and e["Action"] == "pass"}),
    "packageFail": [e["Package"] for e in events if not e.get("Test") and e["Action"] == "fail"],
    "postgresStartedHere": run["postgresStartedHere"],
    "postgresQueryExit": run["postgresQueryExit"],
    "remainingOwnedDatabaseCountAsQueried": (RUN / "db-postgres-cleanup.log").read_text().strip(),
    "postgresStopExit": run["postgresStopExit"],
    "migrationBoundary": "Tested source remains0051 based, no latest-main migration8 or worker integration claim.",
}
assert result["sourceMatchesCurrentGoModule"] and result["sourceUnchangedAsRecordedByRunner"]
assert result["exitCode"] == result["vetExitCode"] == result["postgresQueryExit"] == result["postgresStopExit"] == 0
assert not result["testFail"] and not result["packageFail"]
assert result["remainingOwnedDatabaseCountAsQueried"] == "0"
(OUT / "current-db-check.json").write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
print(json.dumps({**result, "packagePass": len(result["packagePass"])}))
