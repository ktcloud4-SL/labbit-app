"""Export and assert one fresh full fixture result; no network or cloud calls.

Usage: python export_metadata.py <protected fixture directory>
Never copies credentials, addresses, host pins, HTTP/PTY bodies or SSH tuples.
"""
from pathlib import Path
import collections
import hashlib
import json
import sys

HERE = Path(__file__).resolve().parent
PRIVATE = Path(sys.argv[1]).resolve()

def read(name):
    return json.loads((PRIVATE / name).read_text(encoding="utf-8-sig"))

def write(name, value):
    (HERE / name).write_text(json.dumps(value, indent=2, ensure_ascii=False)+"\n", encoding="utf-8")

def hashed(value):
    return hashlib.sha256(value.encode()).hexdigest()

raw = read("ktcloud-control-ledger.json")
resources = raw["resources"]
ledger = {key:raw[key] for key in ("stage","passed","hello","heartbeat","plannedGenerations")}
ledger["labInstanceIdSha256"] = hashed(raw["labInstanceId"])
ledger["resources"] = [{"resourceType":r["resourceType"], "providerIdSha256":hashed(r["providerId"]), "generation":r["generation"], "logicalName":r.get("logicalName"), "observedState":r.get("observedState")} for r in resources]
write("control-ledger.metadata.json", ledger)

bootstrap = read("ktcloud-bootstrap-ledger.json")
bootstrap_refs = [("SERVER",bootstrap["serverId"]), ("KEYPAIR",bootstrap["keypairName"]), ("PUBLIC_IP",bootstrap["publicIpId"]), ("FORWARD",bootstrap["forwardId"])]
bootstrap_refs += [("VOLUME",id) for id in bootstrap["volumeIds"]]
bootstrap_refs += [("POLICY",id) for id in bootstrap["policyIds"]]
bootstrap_metadata = {"deleted":bootstrap["deleted"],"resourceCount":len(bootstrap_refs),"resourceTypeCounts":dict(collections.Counter(kind for kind,id in bootstrap_refs)),"resources":[{"resourceType":kind,"providerIdSha256":hashed(id)} for kind,id in bootstrap_refs]}
write("bootstrap-ledger.metadata.json", bootstrap_metadata)

baseline = read("baseline-inventory.json")
final = read("final-inventory.json")
inventory = {"atUTC":final["atUTC"],"baselinePreserved":final["baselinePreserved"],"differentKinds":final["differentKinds"],"bootstrapDeleted":final["deleted"],"kinds":{}}
for kind in sorted(baseline):
    old, now = sorted(baseline[kind]), sorted(final["inventory"][kind])
    inventory["kinds"][kind] = {"baselineCount":len(old), "finalCount":len(now), "exactIdsEqual":old==now, "baselineIdsSha256":hashed(json.dumps(old,separators=(",",":"))), "finalIdsSha256":hashed(json.dumps(now,separators=(",",":")))}
write("final-inventory.metadata.json", inventory)
residual = read("ktcloud-residual-current.json")
write("final-residual.json", residual)
phases = [json.loads(line) for line in (PRIVATE / "phase-results.jsonl").read_text(encoding="utf-8-sig").splitlines()]
write("phase-results.json", phases)

prefixes = ("Control evidence:", "Production Provider query", "Actual WSS ", "KT Preview ", "Safe Provider error:", "Control acceptance failed:", "Production Connector shutdown:")
lines = [line for line in (PRIVATE / "ktcloud-control-acceptance.log").read_text(encoding="utf-8-sig").splitlines() if line.startswith(prefixes)]
(HERE / "live-lifecycle.log").write_text("\n".join(lines)+"\n", encoding="utf-8")
deployment = []
for path in sorted(PRIVATE.glob("deploy-*.log")):
    deployment.extend(line for line in path.read_text(encoding="utf-8-sig").splitlines() if line.startswith(("Verified deployed ","Deployment:")))
(HERE / "deployment-digests.log").write_text("\n".join(deployment)+"\n", encoding="utf-8")

# Assertions retain a failed result in the exported files, then fail closed.
assert ledger["passed"] is True and ledger["hello"] and ledger["heartbeat"]
assert ledger["plannedGenerations"] == 2
assert ledger["stage"] == "PREVIEW_PRODUCTION_CONNECTOR_GRACEFUL_SHUTDOWN_JOINED_PASS"
assert len(resources) == 10 and len({(r["resourceType"],r["providerId"]) for r in resources}) == 10
assert all(r["observedState"] == "DELETED" for r in resources)
assert collections.Counter(r["generation"] for r in resources) == {1:5,2:5}
assert bootstrap_metadata["deleted"] and len(bootstrap_refs) == 8 and all(id for kind,id in bootstrap_refs)
assert len(set(bootstrap_refs)) == 8
assert inventory["baselinePreserved"] and inventory["bootstrapDeleted"]
assert len(inventory["kinds"]) == 7 and all(k["exactIdsEqual"] for k in inventory["kinds"].values())
assert residual["ownedResidualTotal"] == 0 and all(n == 0 for n in residual["ownedResourceCounts"].values())
expected = ["inventory","create","access","ssh","deploy","control","collect","residual","cleanup","residual","final"]
assert [p["phase"] for p in phases] == expected and all(p["exitCode"] == 0 for p in phases)
assert "Control evidence: KT_PREVIEW_FORWARDING_SMOKE_PASS_OWNED_LAB_RESIDUAL_0" in lines
assert "Production Connector shutdown: exited successfully within 5s; forcedKill=false" in lines
assert not any(line.startswith(("Safe Provider error:","Control acceptance failed:")) for line in lines)
print("Full fixture assertions PASS: true journal, 10 deleted Lab IDs, graceful child exit, 11 phases exit0, zero owned residual, exact seven-kind baseline")
