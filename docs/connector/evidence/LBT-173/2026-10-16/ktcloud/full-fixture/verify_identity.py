"""Read-only cross-check of build, automated tests and live source identities."""
from pathlib import Path
import hashlib
import json

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[6]

def read(name):
    return json.loads((HERE / name).read_text(encoding="utf-8-sig"))

snapshot = read("source-manifest.json")
files = snapshot["files"]
before, after = read("compiled-source-build-before.json"), read("compiled-source-build-after.json")
go_files = {n:v["lf_sha256"] for n,v in files.items() if n.endswith(".go") or n in ("go.mod","go.sum")}
all_compiled = {r["path"]:r["sha256LF"] for r in before["files"]}
compiled = {n:v for n,v in all_compiled.items() if not n.startswith("docs/")}
evidence_fixtures = {n:v for n,v in all_compiled.items() if n.startswith("docs/")}
fixture_differences = sorted(n for n,v in evidence_fixtures.items() if hashlib.sha256((ROOT/n).read_bytes().replace(b"\r\n", b"\n")).hexdigest() != v)
differences = sorted(n for n in set(go_files)|set(compiled) if go_files.get(n) != compiled.get(n))
result = {"branch":snapshot["branch"],"head":snapshot["head"],"sourceFiles":len(files),"compiledFiles":len(all_compiled),"nonDocsCompiledFiles":len(compiled),"historicalEvidenceFixtures":len(evidence_fixtures),"historicalFixtureDifferences":fixture_differences,"buildBeforeAfterEqual":before["files"]==after["files"],"snapshotBuildDifferences":differences,"sourceAlignmentDifferences":read("source-alignment.json")["differences"],"binaryManifestSource":read("binary-manifest.json")["source"]}
(HERE / "identity-verification.json").write_text(json.dumps(result,indent=2)+"\n",encoding="utf-8")
assert result["buildBeforeAfterEqual"] and not differences and not fixture_differences and not result["sourceAlignmentDifferences"]
print("Source/build identity: exact LF equality; no post-build source changes")
