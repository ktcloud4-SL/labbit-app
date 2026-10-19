"""Read-only actual OFF lifecycle reconciliation. Never emit private addresses."""
from collections import Counter
import hashlib
import json
from pathlib import Path

OUT=Path(__file__).resolve().parent
STAGE=OUT.parent/"lbt174-routing-postadmin-20261016/off-lifecycle"
provenance=[]
def raw(name):
    data=(STAGE/name).read_bytes()
    provenance.append({"path":"off-lifecycle/"+name,"bytes":len(data),"sha256":hashlib.sha256(data).hexdigest()})
    return data.decode("utf-8-sig")
def records(name): return [json.loads(line) for line in raw(name).splitlines() if line.startswith("{")]
def check(p):
    assert p["connectCode"]!=0 and p["defaultRoutes"]==[]
    assert p["routeBefore"]==p["routeAfter"]==[]
    return {"connectCode":p["connectCode"],"defaultCount":0,"targetRouteBeforeAfter":[]}
checks={}
for prefix in ("internet-first","after-reset-first"):
    checks[prefix]=[check(records(f"{prefix}-{i:02d}.log")[0]) for i in range(1,13)]
checks["after-reboot"]=check(records("after-reboot.log")[0])
renewals={}
for prefix in ("renew","after-reboot-renew","after-reset-renew"):
    for order in ("lab-first","management-first"):
        name=f"{prefix}-{order}"
        entries=records(name+".log")
        roles=[p["renewedRole"] for p in entries if "renewedRole" in p]
        assert roles==(["lab","management"] if order=="lab-first" else ["management","lab"])
        assert all(p["leaseChanged"] for p in entries if "renewedRole" in p)
        probes=[check(p) for p in entries if "connectCode" in p]
        assert len(probes)==2 and probes[0]==probes[1]
        renewals[name]={"renewedRoles":roles,"leaseChanges":2,"probe":probes[0]}
result=json.loads(raw("probe-run-result.json"))
source={"files":521,"sourceSHA256":"7e8b2cac4e25d20bbb3d44b3a749cfcd0fe350689bf125fda18202bcbd722e31","mismatches":0}
assert result["sourceBefore"]==result["sourceAfter"]==source and result["exitCode"]==0
assert not result["postgresStartedHere"] and result["postgresCleanupSkipped"]=="not_started_here"
events=records("probe-live.log")
tests=Counter(e["Action"] for e in events if e.get("Test"))
assert tests["pass"]==1 and tests["skip"]==tests["fail"]==0
outputs="".join(e.get("Output","") for e in events)
for marker in ("actual reboot boot-id change, authenticated SSH recovery, immutable Reset g2, first12 and both renewal orders verified","Internet OFF fixture exact tracked absence checked: resources=14","Temporary Management security group GET absence confirmed","Temporary integration keypair GET absence confirmed"):
    assert marker in outputs,marker
assert "phase=linux-ci" not in outputs and "phase=linux-apt" not in outputs
report={"source":source,"childExitCode":0,"elapsedSeconds":result["elapsedSeconds"],"testPassEvents":1,"testSkip":0,"testFail":0,
        "routingChecks":checks,"renewals":renewals,"actualRebootBootIDChangeAuthenticatedPinnedSSH":True,
        "immutableResetGeneration2":True,"trackedResourceAbsence":14,"managementSecurityGroupGETAbsence":True,"keypairGETAbsence":True,
        "PostgreSQL":"not used or stopped by this OFF run","nativeCI":"not executed by this OFF run; separate final ON run",
        "scope":"Actual local OpenStack OFF IPv4 defaults and chosen external TCP target, with pinned SSH, reboot/renew/Reset. Not entire Browser/SaaS C3.","rawProvenance":provenance}
(OUT/"final-off-lifecycle-verification.json").write_text(json.dumps(report,indent=2)+"\n",encoding="utf-8")
print(json.dumps({"sourceMismatch":0,"childExit":0,"firstSeries":12,"resetSeries":12,"renewalOrders":6,"trackedAbsence":14,"SGKeypairGETAbsence":True}))
