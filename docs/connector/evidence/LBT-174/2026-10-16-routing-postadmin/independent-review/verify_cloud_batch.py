"""Final actual OpenStack batch raw/source/input reconciliation; read-only."""
from collections import Counter
import hashlib
import json
from pathlib import Path
import re

OUT=Path(__file__).resolve().parent
RUN=OUT.parent/"lbt174-routing-postadmin-20261016"
STAGE=RUN/"final-verified"
REPO=OUT.parent/"labbit-connector-gates-20261015"
def read(p): return json.loads(p.read_text(encoding="utf-8-sig"))
def sha(p): return hashlib.sha256(p.read_bytes()).hexdigest()
log=STAGE/"local-openstack-all.log"
events=[json.loads(line) for line in log.read_text(encoding="utf-8-sig").splitlines() if line.startswith("{")]
tests=Counter(e["Action"] for e in events if e.get("Test"))
packages=Counter(e["Action"] for e in events if not e.get("Test"))
parents=sorted(e["Test"] for e in events if e.get("Test") and "/" not in e["Test"] and e["Action"]=="pass")
expected=sorted(set(re.findall(r"func (TestOpenStack\w+)\(","\n".join(p.read_text(encoding="utf-8-sig") for p in (REPO/"internal/connector/provider/openstack").glob("*test.go")))))
assert parents==expected and len(parents)==12
assert tests["fail"]==tests["skip"]==packages["fail"]==0 and packages["pass"]==1
text="".join(e.get("Output","") for e in events)
assert "WARNING: DATA RACE" not in text
preview_markers=[
"Control OPEN -> outbound Preview WSS -> exact ATTACH -> actual SSH forwarding -> VM HTTP 200 twice; sequential tunnel contract PASS",
"Concurrent session isolation, Control CLOSE, actual PTY/SFTP after Preview close, APP_NOT_RUNNING/PORT_REJECTED/VM_UNREACHABLE PASS",
"Actual Control Reset gen1->gen2 and Cleanup: old WSS closed, logical sessions removed, stale targets denied, fresh generation HTTP PASS",
"Control contains lifecycle metadata only; HTTP/PTY bytes and synthetic credential not present PASS"]
assert all(s in text for s in preview_markers)
linux_required=[
"TestKTFixtureLedgerRejectsSymlinkAndReplacement/symlink",
"TestKTFixtureLedgerRejectsSymlinkAndReplacement/replacement",
"TestSymlinkRejectedAndExcludedFromTree",
"TestInstallCredentialFile/새_credential_file을_만들고_성공_메시지에는_path만_남긴다/POSIX_permission_0600"]
assert all(re.search(r"--- PASS: "+re.escape(s)+r" \(",text) for s in linux_required)
assert "--- SKIP:" not in text
run=read(STAGE/"local-openstack-all-result.json")
batch=read(RUN/"batch-input-result.json")
assert run["exitCode"]==batch["exitCode"]==0
assert run["sourceUnchanged"] is True and batch["sourceUnchanged"] is True
assert run["sourceSHA256"]=="0376a5453a02121e59c526681fe19b9f1ff65f7a273c9fc6e84d8334b4b531a2"
assert batch["sourceBefore"]==batch["sourceAfter"]=={"files":521,"sourceSHA256":"7e8b2cac4e25d20bbb3d44b3a749cfcd0fe350689bf125fda18202bcbd722e31"}
manifest=read(STAGE/"source-manifest.json")
errors=[p for p,h in manifest["files"].items() if hashlib.sha256((REPO/p).read_bytes().replace(b"\r\n",b"\n")).hexdigest()!=h]
assert not errors
preview_output="".join(e.get("Output","") for e in events if e.get("Test")=="TestOpenStackPreviewTransportIntegration")
assert "Exact tracked Lab resource absence checks completed: entries=28" in preview_output
assert "Temporary Management security group GET absence confirmed" in preview_output
assert "Temporary integration keypair GET absence confirmed" in preview_output
cleanup_markers={}
for parent in parents:
    output="".join(e.get("Output","") for e in events if e.get("Test")==parent)
    cleanup_markers[parent]={"managementSGGETAbsence":"Temporary Management security group GET absence confirmed" in output,
                             "keypairGETAbsence":"Temporary integration keypair GET absence confirmed" in output}
    if parent!="TestOpenStackM1Integration":
        assert all(cleanup_markers[parent].values()),parent
binary_errors=[p for p,h in batch["binarySHA256"].items() if sha(STAGE/p)!=h]
assert not binary_errors and len(batch["binarySHA256"])==5
build=read(STAGE/"linux-binaries-build.json")
assert len(build)==5
for item in build:
    assert item["exitCode"]==0
    assert item["sourceBefore"]==item["sourceAfter"]==batch["sourceBefore"]["sourceSHA256"]
    assert item["binarySHA256"]==batch["binarySHA256"][item["name"]]
    assert (STAGE/item["name"]).read_bytes()[:4]==b"\x7fELF"
report={"sourceFiles":521,"sourceSHA256":batch["sourceBefore"]["sourceSHA256"],"currentSourceMismatch":errors,
        "childExit":0,"batchWrapperExit":0,"testPassEvents":tests["pass"],"testParentPass":len(parents),"parentNames":parents,
        "testSkip":0,"testFail":0,"packagePass":1,"raceWarnings":0,"raceFlagUsed":False,
        "requiredPreviewMarkers":preview_markers,"linuxFilesystemExactPASSNames":linux_required,
        "previewTrackedLabAbsenceEntries":28,"auxiliaryCleanupMarkersByParent":cleanup_markers,
        "linuxBinaries":batch["binarySHA256"],"linuxBinaryHashMismatches":binary_errors,"linuxBinaryBuildMetadataSHA256":sha(STAGE/"linux-binaries-build.json"),
        "actualRun":run,"batchWrapper":batch,"rawSHA256":sha(log),"rawBytes":log.stat().st_size,
        "scope":"Actual local OpenStack/VM/SSH+Mock Control/Data Gateway 12 parent fixtures; Linux filesystem binaries are CGO0/non-race. Native C1 CGO1/race is separate. Whole SaaS/Browser C3 and KT are excluded.",
        "cleanupBoundary":"Per-fixture known exact IDs and temporary Management SG/keypair GET absence; no claim all arbitrary cloud resources are absent."}
(OUT/"final-cloud-batch-verification.json").write_text(json.dumps(report,ensure_ascii=False,indent=2)+"\n",encoding="utf-8")
print(json.dumps({"parents":len(parents),"passEvents":tests["pass"],"skip":0,"fail":0,"childExit":0,"sourceMismatches":0,"binaryHashes":len(batch["binarySHA256"])}))
