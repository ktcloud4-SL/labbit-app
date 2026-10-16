"""Independently inspect completed native-only raw evidence, no external calls."""
import hashlib
import json
import pathlib
import re

OUT = pathlib.Path(__file__).resolve().parent
RUN = OUT.parent / "lbt174-routing-fix-20261016/native-independent"
REPO = OUT.parent / "labbit-connector-gates-20261015"
identity = json.loads((OUT / "source-current-identity.json").read_text(encoding="utf-8"))
meta = json.loads((RUN / "native-run-result.json").read_text(encoding="utf-8"))
raw = (RUN / "linux-ci.log").read_text(encoding="utf-8-sig")
events = [json.loads(line) for line in raw.splitlines() if line.startswith("{")]
native_source = [e for e in events if "sourceFiles" in e]
tests = [e for e in events if e.get("Test")]
required = sorted({name for path in (REPO / "internal/server/app").glob("*_test.go")
                   for name in re.findall(r"^func (TestC1\w+)\(", path.read_text(encoding="utf-8"), re.MULTILINE)})
passed = sorted({e["Test"] for e in tests if e["Action"] == "pass" and "/" not in e["Test"]})
outer = [json.loads(line) for line in (RUN / "native-live.log").read_text(encoding="utf-8-sig").splitlines() if line.strip()]
outputs = "".join(e.get("Output", "") for e in outer)
scripts = {name: hashlib.sha256((RUN / name).read_bytes()).hexdigest()
           for name in ["overlay.json", "local_probe_test.go", "linux_ci.sh"]}
archive = json.loads((RUN / "linux-source-archive.json").read_text(encoding="utf-8"))
archive_actual = hashlib.sha256((RUN / "linux-source.tar.gz").read_bytes()).hexdigest()
go_actual = hashlib.sha256((RUN / "go1.27.1.linux-amd64.tar.gz").read_bytes()).hexdigest()
result = {
    "sourceSHA256": identity["sourceSHA256"],
    "sourceBeforeMatchesCurrent": meta["sourceBefore"]["sourceSHA256"] == identity["sourceSHA256"] and meta["sourceBefore"]["mismatches"] == 0,
    "sourceAfterMatchesCurrent": meta["sourceAfter"]["sourceSHA256"] == identity["sourceSHA256"] and meta["sourceAfter"]["mismatches"] == 0,
    "nativeRawSourceObservation": native_source,
    "overlayInputsMatchAsRun": scripts == meta["overlaySHA256"],
    "nativeArchiveSHA256": archive_actual, "nativeArchiveMatchesManifest": archive_actual == archive["archiveSHA256"],
    "goArchiveSHA256": go_actual, "goArchiveMatchesPinnedRunnerValue": go_actual == "63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445",
    "toolchainPreamble": raw.splitlines()[:7],
    "completionMarkers": {name: raw.splitlines().count("LBT174_LINUX_" + name + "_PASS") for name in ["VET", "BUILD", "RACE"]},
    "requiredParentNamesFromCurrentSource": required,
    "observedPassedParentNames": passed,
    "testPassEventsIncludingSubtests": sum(e["Action"] == "pass" for e in tests),
    "testSkip": [e["Test"] for e in tests if e["Action"] == "skip"],
    "testFail": [e["Test"] for e in tests if e["Action"] == "fail"],
    "packagePass": [e["Package"] for e in events if "Action" in e and not e.get("Test") and e["Action"] == "pass"],
    "packageFail": [e["Package"] for e in events if "Action" in e and not e.get("Test") and e["Action"] == "fail"],
    "dataRaceWarnings": raw.count("WARNING: DATA RACE"),
    "outerExitCode": meta["exitCode"], "outerElapsedSeconds": meta["elapsedSeconds"],
    "outerTestPass": [e["Test"] for e in outer if e.get("Test") and e["Action"] == "pass"],
    "outerFailures": [e for e in outer if e["Action"] == "fail"],
    "nativeOnlyScopeLogged": "INDEPENDENT_NATIVE_ONLY" in outputs,
    "trackedAbsenceCount": int(re.search(r"exact tracked absence checked: resources=(\d+)", outputs).group(1)),
    "managementSGExactGETAbsenceLogged": "Temporary Management security group GET absence confirmed" in outputs,
    "keypairExactGETAbsenceLogged": "Temporary integration keypair GET absence confirmed" in outputs,
    "aptOwnedRouteAbsenceLogged": '"aptOwnedRouteAbsent": true' in (RUN / "linux-apt.log").read_text(encoding="utf-8"),
    "postgresQueryExit": meta["postgresQueryExit"], "postgresStopExit": meta["postgresStopExit"],
    "remainingOwnedDatabaseCountAsQueried": (RUN / "postgres-cleanup.log").read_text(encoding="utf-8").strip(),
    "reviewerExecution": "Read original results; no VM/cloud/service/PG mutation or native command execution by reviewer.",
    "scope": "Native Linux CGO1 CI-equivalent tagged C1 with Mock Provider and real loopback-forwarded PostgreSQL. Routing/renew/reboot/Reset acceptance excluded. Latest-main migration8/worker integration not executed.",
}
assert result["sourceBeforeMatchesCurrent"] and result["sourceAfterMatchesCurrent"] and result["overlayInputsMatchAsRun"]
assert len(native_source) == 1 and native_source[0]["sourceSHA256"] == identity["sourceSHA256"] and native_source[0]["mismatches"] == 0
assert result["nativeArchiveMatchesManifest"] and result["goArchiveMatchesPinnedRunnerValue"]
assert len(required) == 12 and passed == required
assert result["outerExitCode"] == result["postgresQueryExit"] == result["postgresStopExit"] == 0
assert all(n == 1 for n in result["completionMarkers"].values())
assert not result["testSkip"] and not result["testFail"] and not result["packageFail"] and not result["dataRaceWarnings"] and not result["outerFailures"]
assert result["nativeOnlyScopeLogged"] and result["trackedAbsenceCount"] == 8
assert result["managementSGExactGETAbsenceLogged"] and result["keypairExactGETAbsenceLogged"] and result["aptOwnedRouteAbsenceLogged"]
assert result["remainingOwnedDatabaseCountAsQueried"] == "0"
(OUT / "native-independent-check.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(json.dumps({"parents": len(passed), "testPassEvents": result["testPassEventsIncludingSubtests"], "skip": len(result["testSkip"]), "fail": len(result["testFail"]), "raceWarnings": result["dataRaceWarnings"], "exitCode": result["outerExitCode"], "sourceIdentity": "PASS", "cleanup": "8tracked+SG+keypair+DB0/stop0"}))
