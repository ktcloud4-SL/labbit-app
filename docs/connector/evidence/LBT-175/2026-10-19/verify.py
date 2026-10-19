"""Local source identity and allowlisted KT fixture metadata; never calls cloud APIs.

python verify.py source OUTPUT LABEL
python verify.py summarize GO_JSON_LOG OUTPUT_JSON
python verify.py export PROTECTED_FIXTURE PHASE_JSONL OUTPUT
"""
from pathlib import Path
from datetime import datetime, timezone
import collections
import hashlib
import json
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[5]

def write(path, value):
    Path(path).write_text(json.dumps(value, indent=2, ensure_ascii=False)+"\n", encoding="utf-8")

def digest(value):
    return hashlib.sha256(value.encode()).hexdigest()

def source(output, label):
    def git(*args):
        return subprocess.check_output(["git", *args], cwd=ROOT).decode().strip()
    names=set(git("ls-files").splitlines()+git("ls-files","--others","--exclude-standard").splitlines())
    files={}
    for name in sorted(names):
        if name.startswith("docs/"): continue
        if name.endswith(".go") or name in ("go.mod","go.sum","web/package.json","web/package-lock.json") or name.startswith(("contracts/","runtime/","web/src/")):
            files[name]=hashlib.sha256((ROOT/name).read_bytes().replace(b"\r\n",b"\n")).hexdigest()
    result={"atUTC":datetime.now(timezone.utc).isoformat(),"label":label,"base":"89a698dac50a8705ed305a5eccf94ee9505b033f","head":git("rev-parse","HEAD"),"branch":git("branch","--show-current"),"files":files,"canonicalSHA256":digest(json.dumps(files,sort_keys=True,separators=(",",":")))}
    write(Path(output)/(label+"-source.json"),result)
    print(json.dumps({"files":len(files),"canonicalSHA256":result["canonicalSHA256"]}))

def summarize(log,output):
    events=[]
    for line in Path(log).read_text(encoding="utf-8-sig").splitlines():
        try: events.append(json.loads(line))
        except json.JSONDecodeError: pass
    results=[e for e in events if e.get("Action") in ("pass","fail","skip")]
    summary={"packages":dict(collections.Counter(e["Action"] for e in results if not e.get("Test"))),"tests":dict(collections.Counter(e["Action"] for e in results if e.get("Test"))),"skipped":[{"package":e["Package"],"test":e["Test"]} for e in results if e.get("Test") and e["Action"]=="skip"],"failed":[{"package":e["Package"],"test":e.get("Test")} for e in results if e["Action"]=="fail"]}
    write(output,summary)
    print(json.dumps({k:summary[k] for k in ("packages","tests")}))

def export(private,phases,output):
    private,output=Path(private),Path(output)
    def read(name): return json.loads((private/name).read_text(encoding="utf-8-sig"))
    raw=read("ktcloud-control-ledger.json")
    resources=raw["resources"]
    journal={k:raw[k] for k in ("stage","passed","hello","heartbeat","plannedGenerations")}
    journal["resources"]=[{"resourceType":r["resourceType"],"providerIdSHA256":digest(r["providerId"]),"generation":r["generation"],"observedState":r.get("observedState")} for r in resources]
    write(output/"control-ledger.metadata.json",journal)
    bootstrap=read("ktcloud-bootstrap-ledger.json")
    refs=[("SERVER",bootstrap["serverId"]),("KEYPAIR",bootstrap["keypairName"]),("PUBLIC_IP",bootstrap["publicIpId"]),("FORWARD",bootstrap["forwardId"])]
    refs += [("VOLUME",x) for x in bootstrap["volumeIds"]]+[("POLICY",x) for x in bootstrap["policyIds"]]
    write(output/"bootstrap-ledger.metadata.json",{"deleted":bootstrap["deleted"],"resourceCount":len(refs),"resources":[{"resourceType":kind,"providerIdSHA256":digest(value)} for kind,value in refs]})
    baseline,final=read("baseline-inventory.json"),read("final-inventory.json")
    inventory={"atUTC":final["atUTC"],"baselinePreserved":final["baselinePreserved"],"bootstrapDeleted":final["deleted"],"differentKinds":final["differentKinds"],"kinds":{}}
    for kind in sorted(baseline):
        old,now=sorted(baseline[kind]),sorted(final["inventory"][kind])
        inventory["kinds"][kind]={"baselineCount":len(old),"finalCount":len(now),"exactIDsEqual":old==now,"baselineIDsSHA256":digest(json.dumps(old,separators=(",",":"))),"finalIDsSHA256":digest(json.dumps(now,separators=(",",":")))}
    write(output/"final-inventory.metadata.json",inventory)
    residual=read("ktcloud-residual-current.json");write(output/"final-residual.json",residual)
    phase_results=[json.loads(line) for line in Path(phases).read_text(encoding="utf-8-sig").splitlines()]
    write(output/"phase-results.json",phase_results)
    preparation_failures=[p for p in phase_results if p["ExitCode"]!=0]
    completed=[p for p in phase_results if p["ExitCode"]==0]
    write(output/"preparation-attempts.json",{"initialFailures":preparation_failures,"retriesAreReadOnlySSH":all(p["Phase"]=="ssh" for p in preparation_failures),"allAttemptsRetained":True})
    prefixes=("Control evidence:","Production Provider query", "Actual WSS ","KT Preview transport ","Safe Provider error:","Control acceptance failed:","Production Connector shutdown:")
    lines=[line for line in (private/"ktcloud-control-acceptance.log").read_text(encoding="utf-8-sig").splitlines() if line.startswith(prefixes)]
    (output/"live-lifecycle.log").write_text("\n".join(lines)+"\n",encoding="utf-8")
    deployment=[]
    for path in private.glob("deploy-*.log"):
        deployment.extend(line for line in path.read_text(encoding="utf-8-sig").splitlines() if line.startswith(("Verified deployed ","Deployment:")))
    (output/"deployment-digests.log").write_text("\n".join(deployment)+"\n",encoding="utf-8")
    checks={
      "journalPassed":journal["passed"] is True and journal["hello"] and journal["heartbeat"] and journal["plannedGenerations"]==2,
      "gracefulChildExit":journal["stage"]=="PREVIEW_PRODUCTION_CONNECTOR_GRACEFUL_SHUTDOWN_JOINED_PASS" and "Production Connector shutdown: exited successfully within 5s; forcedKill=false" in lines,
      "tenDeletedLabIDs":len(resources)==10 and len({(r["resourceType"],r["providerId"]) for r in resources})==10 and all(r["observedState"]=="DELETED" for r in resources),
      "eightBootstrapResourcesDeleted":bootstrap["deleted"] is True and len(refs)==8 and len(set(refs))==8 and all(value for kind,value in refs),
      "sevenInventoriesMatch":inventory["baselinePreserved"] is True and inventory["bootstrapDeleted"] is True and len(inventory["kinds"])==7 and all(v["exactIDsEqual"] for v in inventory["kinds"].values()),
      "zeroOwnedResidual":residual["ownedResidualTotal"]==0 and all(n==0 for n in residual["ownedResourceCounts"].values()),
      "requiredStagesFinallyPassed":[p["Phase"] for p in completed]==["create","access","ssh","deploy","control","collect","residual","cleanup","residual","final"] and all(p["Phase"]=="ssh" for p in preparation_failures),
      "transportGatePassed":"Control evidence: KT_PREVIEW_TRANSPORT_WSS_HTTP_LIFECYCLE_PASS_OWNED_LAB_RESIDUAL_0" in lines,
      "noLifecycleError":not any(line.startswith(("Safe Provider error:","Control acceptance failed:")) for line in lines),
    }
    write(output/"live-verification.json",checks)
    print(json.dumps(checks))
    if not all(checks.values()): raise SystemExit(1)

if __name__=="__main__":
    if sys.argv[1]=="source": source(*sys.argv[2:])
    elif sys.argv[1]=="summarize": summarize(*sys.argv[2:])
    elif sys.argv[1]=="export": export(*sys.argv[2:])
    else: raise SystemExit("source|summarize|export")
