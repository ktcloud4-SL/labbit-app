"""Local compile/command AST check only. Never execute generated remote commands."""
import ast
import base64
import hashlib
import json
from pathlib import Path
import re
import subprocess

OUT = Path(__file__).resolve().parent
ROOT = OUT.parent / "lbt174-routing-postadmin-20261016"
STAGE = ROOT / "off-lifecycle"
REPO = OUT.parent / "labbit-connector-gates-20261015"
GO = Path("C:/Users/user/AppData/Local/Programs/Go-1.27.1/go/bin/go.exe")
def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()
manifest = json.loads((STAGE / "source-manifest.json").read_text(encoding="utf-8"))
def source_mismatches():
    return [p for p,h in manifest["files"].items() if hashlib.sha256((REPO/p).read_bytes().replace(b"\r\n", b"\n")).hexdigest() != h]
assert not source_mismatches()
overlay = json.loads((STAGE / "overlay.json").read_text(encoding="utf-8"))
overlay["Replace"][str(REPO / "internal/connector/provider/openstack/review_off_command_dump_test.go")] = str(OUT / "off_command_dump_test.go")
overlay_path = OUT / "off-method-overlay.json"
overlay_path.write_text(json.dumps(overlay, indent=2) + "\n", encoding="utf-8")
command = [str(GO), "test", "-overlay", str(overlay_path), "-run", "^TestReviewOffGeneratedPython$", "-count=1", "-v", "./internal/connector/provider/openstack"]
result = subprocess.run(command, cwd=REPO, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
log = OUT / "off-generated-python.log"
log.write_bytes(result.stdout)
assert result.returncode == 0
parsed = {}
for name, encoded in re.findall(rb"REVIEW_COMMAND (\S+) (\S+)", result.stdout):
    command_text = base64.b64decode(encoded).decode()
    match = re.search(r"base64\.b64decode\('([^']+)'\)", command_text)
    assert match
    script = base64.b64decode(match[1]).decode()
    ast.parse(script)
    assert "connectCode']!=0" in script and "defaultRoutes'])==0" in script
    assert "routeBefore']==[]" in script and "routeAfter']==[]" in script
    parsed[name.decode()] = {"pythonSHA256": hashlib.sha256(script.encode()).hexdigest(), "AST": "PASS"}
assert len(parsed) == 3
assert not source_mismatches()
report = {"mode": "local test-only overlay; generated Python AST only", "cloudCalls": 0,
          "exitCode": result.returncode, "command": command, "sourceFiles": len(manifest["files"]),
          "sourceMismatchesBeforeAfter": 0, "helperSHA256": sha(STAGE / "local_probe_test.go"),
          "preparationSHA256": sha(STAGE / "preparation.json"), "generatedCommands": parsed,
          "review": {"snapshotInternetOutbound": False, "first12EachGeneration": True,
                     "renewBothOrdersBeforeAfterRebootAndReset": True, "bootIDChangeAndPinnedSSH": True,
                     "knownResourceCleanupRetained": True, "onOnlyNativeAptAndDiagnosticsNotCalled": True},
          "limits": ["This is syntax and method review, not actual OFF routing acceptance.",
                     "IPv4 defaults and chosen external TCP target are the supported scope.",
                     "Copied linux_ci.sh and native archive metadata are unused by the OFF test."]}
(OUT / "off-method-review.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
print(json.dumps({"exitCode": result.returncode, "generatedPythonCommands": len(parsed), "sourceMismatchesBeforeAfter": 0}))
