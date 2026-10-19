"""Export reviewed metadata only from a protected fixture directory.

No cloud calls. Credentials, addresses, host pins, HTTP/PTY bytes and SSH tuples
are deliberately never read. Provider IDs are represented by SHA256 for pairing.
"""
from pathlib import Path
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

def ledger_metadata(name):
    raw = read(name)
    return {"stage":raw["stage"],"passed":raw["passed"],"hello":raw["hello"],"heartbeat":raw["heartbeat"],"plannedGenerations":raw["plannedGenerations"],"labInstanceIdSha256":hashed(raw["labInstanceId"]),"resources":[{"resourceType":r["resourceType"],"providerIdSha256":hashed(r["providerId"]),"generation":r["generation"],"logicalName":r.get("logicalName"),"observedState":r.get("observedState")} for r in raw["resources"]]}

write("original-control-ledger.metadata.json", ledger_metadata("original-ktcloud-control-ledger.json"))
write("recovered-control-ledger.metadata.json", ledger_metadata("recovered-ktcloud-control-ledger.json"))
for source, target in [("original-ktcloud-residual-current.json","initial-residual.json"),("before-bootstrap-ktcloud-residual.json","before-bootstrap-residual.json"),("ktcloud-residual-current.json","final-residual.json")]:
    write(target, read(source))

baseline = read("baseline-inventory.json")
final = read("final-inventory.json")
current = final["inventory"]
inventory = {"atUTC":final["atUTC"],"baselinePreserved":final["baselinePreserved"],"differentKinds":final["differentKinds"],"bootstrapDeleted":final["deleted"],"kinds":{}}
for kind in sorted(baseline):
    old, now = sorted(baseline[kind]), sorted(current[kind])
    inventory["kinds"][kind] = {"baselineCount":len(old),"finalCount":len(now),"exactIdsEqual":old==now,"baselineIdsSha256":hashed(json.dumps(old,separators=(",",":"))),"finalIdsSha256":hashed(json.dumps(now,separators=(",",":")))}
write("final-inventory.metadata.json",inventory)

phases = [json.loads(line) for line in (PRIVATE / "phase-results.jsonl").read_text(encoding="utf-8-sig").splitlines()]
write("phase-results.json",phases)
prefixes = ("Control evidence:", "Production Provider query", "Actual WSS ", "KT Preview ", "Safe Provider error:", "Control acceptance failed:", "Production Connector shutdown:")
recovery = []
for path in sorted(PRIVATE.glob("ktcloud-control-recovery-*.log")):
    recovery.append("Protected log: " + path.name)
    recovery.extend(line for line in path.read_text(encoding="utf-8-sig").splitlines() if line.startswith(prefixes))
(HERE / "recovery-lifecycle.log").write_text("\n".join(recovery)+"\n", encoding="utf-8")
deployment = []
for path in sorted(PRIVATE.glob("deploy-*.log")):
    deployment.append("Protected log: " + path.name)
    deployment.extend(line for line in path.read_text(encoding="utf-8-sig").splitlines() if line.startswith(("Verified deployed ","Deployment:")))
(HERE / "deployment-digests.log").write_text("\n".join(deployment)+"\n", encoding="utf-8")
live = json.loads((HERE / "live-source-manifest.json").read_text(encoding="utf-8"))["files"]
final_source = json.loads((HERE / "source-manifest.json").read_text(encoding="utf-8"))["files"]
write("live-to-final-source-differences.json", {"differences":[name for name in sorted(set(live)|set(final_source)) if live.get(name)!=final_source.get(name)]})
assert inventory["baselinePreserved"] and inventory["bootstrapDeleted"]
assert len(inventory["kinds"]) == 7 and all(k["exactIdsEqual"] for k in inventory["kinds"].values())
assert read("ktcloud-residual-current.json")["ownedResidualTotal"] == 0
print("Reviewed metadata exported; exact seven-kind baseline and zero tracked residual asserted")
