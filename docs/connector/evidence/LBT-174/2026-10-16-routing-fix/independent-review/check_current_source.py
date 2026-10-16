import hashlib
import json
import pathlib
import subprocess

root = pathlib.Path("C:/Users/user/Documents/ChatGPT/3차 2")
repo = root / "tmp/labbit-connector-gates-20261015"
out = root / "tmp/lbt174-routing-fix-independent-20261016"
current = json.loads((root / "tmp/lbt174-routing-fix-20261016/source-manifest.json").read_text(encoding="utf-8-sig"))
old = json.loads((root / "tmp/lbt174-followup-20261016/finalized/source-manifest.json").read_text(encoding="utf-8-sig"))
actual = {name: hashlib.sha256((repo / name).read_bytes().replace(b"\r\n", b"\n")).hexdigest() for name in current["files"]}
go_inputs = {name: sha for name, sha in actual.items() if name.endswith(".go") or name in {"go.mod", "go.sum"}}
mismatches = [name for name in actual if actual[name] != current["files"][name]]
git = lambda *args: subprocess.check_output(["git", "-C", str(repo), *args], text=True, encoding="utf-8").strip()
tracked = git("diff", "--name-only").splitlines()
untracked = [p for p in git("ls-files", "--others", "--exclude-standard").splitlines() if not p.startswith("docs/connector/evidence/")]
paths = sorted(tracked + untracked)
rows = []
for name in paths:
    rows.append({"path": name, "status": "untracked" if name in untracked else "modified",
                 "lfSHA256": hashlib.sha256((repo / name).read_bytes().replace(b"\r\n", b"\n")).hexdigest()})
result = {"base": current["base"], "head": git("rev-parse", "HEAD"), "branch": git("branch", "--show-current"),
          "files": len(actual), "sourceSHA256": hashlib.sha256(json.dumps(actual, sort_keys=True).encode()).hexdigest(),
          "goModuleFiles": len(go_inputs), "goModuleSHA256": hashlib.sha256(json.dumps(go_inputs, sort_keys=True).encode()).hexdigest(),
          "manifestMismatch": mismatches, "stagedPaths": git("diff", "--cached", "--name-only").splitlines(),
          "previous502Changed": sorted(p for p, sha in old["files"].items() if actual.get(p) != sha),
          "newManifestPaths": sorted(set(actual) - set(old["files"])), "changedScopeCount": len(paths), "changedScope": rows,
          "evidenceCurrentPackageNotResealedByReviewer": True}
(out / "source-current-identity.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(json.dumps({k: v for k, v in result.items() if k != "changedScope"}, ensure_ascii=False))
assert not mismatches
