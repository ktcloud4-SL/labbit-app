"""Read-only final ON lifecycle reconciliation; no private addresses emitted."""
from collections import Counter
import hashlib
import json
from pathlib import Path

OUT = Path(__file__).resolve().parent
STAGE = OUT.parent / "lbt174-routing-postadmin-20261016/final-verified"
provenance = []
def raw(name):
    data = (STAGE / name).read_bytes()
    provenance.append({"path": "final-verified/"+name, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()})
    return data.decode("utf-8-sig")
def records(name): return [json.loads(line) for line in raw(name).splitlines() if line.startswith("{")]
def check(p):
    assert p["connectCode"] == 0 and p["socketSourceRole"] == "lab"
    assert [r["role"] for r in p["defaultRoutes"]] == ["lab"]
    assert [r["role"] for r in p["routeBefore"]] == ["lab"]
    assert [r["role"] for r in p["routeAfter"]] == ["lab"]
    assert [r["role"] for r in p["socketTupleRoute"]] == ["lab"]
    return {"connectCode": p["connectCode"], "sourceRole": p["socketSourceRole"], "defaultRoles": [r["role"] for r in p["defaultRoutes"]]}
checks = {}
for prefix in ("internet-first", "after-reset-first"):
    checks[prefix] = [check(records(f"{prefix}-{i:02d}.log")[0]) for i in range(1,13)]
for name in ("after-timeout", "after-reboot"):
    checks[name] = check(records(name+".log")[0])
renewals = {}
for prefix in ("renew", "after-reboot-renew", "after-reset-renew"):
    for order in ("lab-first", "management-first"):
        name = f"{prefix}-{order}"
        entries = records(name+".log")
        roles = [p["renewedRole"] for p in entries if "renewedRole" in p]
        assert roles == (["lab","management"] if order == "lab-first" else ["management","lab"])
        assert all(p["leaseChanged"] for p in entries if "renewedRole" in p)
        probes = [check(p) for p in entries if "connectCode" in p]
        assert len(probes) == 2
        renewals[name] = {"renewedRoles": roles, "leaseChanges": 2, "probe": probes[0], "sameFinalEmission": probes[0] == probes[1]}
timeout = records("diagnostic-timeout-cleanup.log")[0]
assert timeout == {"intentionalTimeoutObserved": True, "ownedRouteAbsent": True, "cleanupComplete": True}
ab = records("route-counterfactual.log")
forced = {p["case"]: p for p in ab if p.get("case", "").startswith("forced-")}
assert forced["forced-management"]["connectCode"] != 0
assert forced["forced-management"]["socketSourceRole"] == "management"
assert forced["forced-lab"]["connectCode"] == 0 and forced["forced-lab"]["socketSourceRole"] == "lab"
assert next(p for p in ab if p.get("case") == "restored")["ownedRouteAbsent"] is True
apt = records("linux-apt.log")
assert any(p.get("aptTemporaryRouteUsed") is False for p in apt)
result = json.loads(raw("native-run-result.json"))
expected_source = {"files": 521, "sourceSHA256": "7e8b2cac4e25d20bbb3d44b3a749cfcd0fe350689bf125fda18202bcbd722e31", "mismatches": 0}
assert result["sourceBefore"] == result["sourceAfter"] == expected_source
assert result["exitCode"] == result["postgresQueryExit"] == result["postgresStopExit"] == 0
assert result["postgresStartedHere"] is True
assert raw("postgres-cleanup.log").strip() == "0"
events = records("native-live.log")
tests = Counter(e["Action"] for e in events if e.get("Test"))
assert tests["pass"] == 1 and tests["skip"] == tests["fail"] == 0
outputs = "".join(e.get("Output", "") for e in events)
for marker in ("actual reboot boot-id change, authenticated SSH recovery, immutable Reset g2, first12 and both renewal orders verified", "exact tracked absence checked: resources=16", "Temporary Management security group GET absence confirmed", "Temporary integration keypair GET absence confirmed"):
    assert marker in outputs, marker
report = {"source": expected_source, "childExitCode": 0, "elapsedSeconds": result["elapsedSeconds"],
          "testPassEvents": 1, "testSkip": 0, "testFail": 0, "routingChecks": checks,
          "renewals": renewals, "intentionalTimeout": timeout,
          "AB": {"forcedManagementTCPFailed": True, "forcedManagementSource": "management", "forcedLabTCPSucceeded": True, "forcedLabSource": "lab", "ownedHostRouteAbsentAndOriginalRouteRestored": True},
          "aptTemporaryRouteUsed": False, "actualRebootBootIDChangeAuthenticatedPinnedSSH": True,
          "immutableResetGeneration2": True, "trackedResourceAbsence": 16,
          "managementSecurityGroupGETAbsence": True, "keypairGETAbsence": True,
          "PostgreSQL": {"startedHere": True, "temporaryDBs": 0, "queryExit": 0, "stopExit": 0},
          "nativeCI": "See native-ci-raw-verification.json: exact12parents/67pass/0skip/0race",
          "scope": "Actual local OpenStack ON lifecycle with native C1 (real PostgreSQL/Mock Provider). OFF, all12 actual fixture parents and packaging are separate.",
          "rawProvenance": provenance}
(OUT / "final-on-lifecycle-verification.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
print(json.dumps({"sourceMismatch": 0, "childExit": 0, "firstSeries": 12, "resetSeries": 12, "renewalOrders": len(renewals), "trackedAbsence": 16, "SGKeypairGETAbsence": True, "PGDBs": 0}))
