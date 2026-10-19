"""Read-only source identity and test log capture; does not contact the cloud."""
from pathlib import Path
from datetime import datetime, timezone
import collections
import gzip
import hashlib
import json
import subprocess
import sys

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[6]

def git(*args):
    return subprocess.check_output(["git", *args], cwd=ROOT)

def sources():
    names = set(git("ls-files").decode().splitlines() + git("ls-files", "--others", "--exclude-standard").decode().splitlines())
    selected = {}
    for name in sorted(names):
        if name.startswith("docs/"):
            continue
        if name.endswith(".go") or name in ("go.mod", "go.sum", "web/package.json", "web/package-lock.json") or name.startswith(("runtime/", "contracts/", "web/src/")):
            raw = (ROOT / name).read_bytes()
            lf = raw.replace(b"\r\n", b"\n")
            selected[name] = {"sha256": hashlib.sha256(raw).hexdigest(), "lf_sha256": hashlib.sha256(lf).hexdigest()}
    return selected

mode = sys.argv[1]
if mode == "snapshot":
    files = sources()
    manifest = {"captured_utc": datetime.now(timezone.utc).isoformat(), "branch": git("branch", "--show-current").decode().strip(), "base": "88097be5d9bb6996b718fd185515ca324629fda6", "head": git("rev-parse", "HEAD").decode().strip(), "source_state": "local uncommitted branch", "files": files}
    (HERE / "source-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    patch = git("diff", "--no-ext-diff", "HEAD", "--", "internal", "runtime", "tools")
    for name in git("ls-files", "--others", "--exclude-standard").decode().splitlines():
        if name.startswith(("internal/", "tools/")) and name.endswith(".go"):
            result = subprocess.run(["git", "diff", "--no-index", "--", "/dev/null", name], cwd=ROOT, stdout=subprocess.PIPE)
            if result.returncode not in (0,1):
                raise SystemExit("diff failed")
            patch += result.stdout
    (HERE / "source-diff.patch").write_bytes(patch)
    print(f"Source snapshot: {len(files)} files")
elif mode == "check":
    expected = json.loads((HERE / "source-manifest.json").read_text(encoding="utf-8"))["files"]
    actual = sources()
    differences = sorted(name for name in set(expected) | set(actual) if expected.get(name) != actual.get(name))
    (HERE / "source-alignment.json").write_text(json.dumps({"checked_utc": datetime.now(timezone.utc).isoformat(), "files": len(actual), "differences": differences}, indent=2)+"\n", encoding="utf-8")
    print(f"Source alignment: {len(differences)} differences")
    if differences: raise SystemExit(1)
elif mode == "summarize":
    path = HERE / "go-test.jsonl"
    events = []
    if path.exists():
        raw = path.read_bytes()
        (HERE / "go-test.jsonl.gz").write_bytes(gzip.compress(raw, mtime=0))
    else:
        raw = gzip.decompress((HERE / "go-test.jsonl.gz").read_bytes())
    for line in raw.decode("utf-8-sig").splitlines():
        try: events.append(json.loads(line))
        except json.JSONDecodeError: pass
    summary = {"packages": dict(collections.Counter(e["Action"] for e in events if e.get("Action") in ("pass","fail","skip") and not e.get("Test"))), "tests": dict(collections.Counter(e["Action"] for e in events if e.get("Action") in ("pass","fail","skip") and e.get("Test"))), "skipped": [{"package":e["Package"],"test":e["Test"]} for e in events if e.get("Action")=="skip" and e.get("Test")]}
    (HERE / "go-test-summary.json").write_text(json.dumps(summary, indent=2)+"\n", encoding="utf-8")
    print(json.dumps(summary["packages"])); print(json.dumps(summary["tests"]))
elif mode == "format":
    files = sources()
    bad, raw_differences = [], 0
    for name in files:
        if not name.endswith(".go"): continue
        raw = (ROOT / name).read_bytes()
        lf = raw.replace(b"\r\n", b"\n")
        formatted = subprocess.check_output(["gofmt"], input=lf, cwd=ROOT)
        if formatted != lf: bad.append(name)
        if formatted != raw: raw_differences += 1
    previous = json.loads((HERE.parent / "source-manifest.json").read_text(encoding="utf-8"))["files"]
    core_changed = sorted(name for name in previous if name.startswith(("internal/", "runtime/", "contracts/", "web/")) and previous[name] != files.get(name))
    result = {"go_files": sum(n.endswith(".go") for n in files), "canonical_unformatted": bad, "inherited_raw_crlf_differences": raw_differences, "production_runtime_contract_web_differences_from_local_audit": core_changed}
    (HERE / "format-and-local-alignment.json").write_text(json.dumps(result, indent=2)+"\n", encoding="utf-8")
    print(json.dumps(result))
    if bad: raise SystemExit(1)
else:
    raise SystemExit("snapshot|check|summarize|format")

