"""Capture dirty-source identity and summarize existing logs; never runs cloud mutations."""
from pathlib import Path
from datetime import datetime, timezone
import gzip
import hashlib
import json
import subprocess

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[4]


def git(*args):
    return subprocess.check_output(["git", *args], cwd=ROOT).decode("utf-8").strip()


def source_file(name):
    return name.endswith(".go") or name in ("go.mod", "go.sum") or name.startswith(("contracts/", "runtime/", "web/src/")) or name in ("web/package.json", "web/package-lock.json")


files = sorted(set(git("ls-files").splitlines() + git("ls-files", "--others", "--exclude-standard").splitlines()))
source = {}
bad_format = []
raw_format_count = 0
for name in files:
    if not source_file(name) or name.startswith("docs/"):
        continue
    data = (ROOT / name).read_bytes()
    lf = data.replace(b"\r\n", b"\n")
    source[name] = {"sha256": hashlib.sha256(data).hexdigest(), "lf_sha256": hashlib.sha256(lf).hexdigest()}
    if name.endswith(".go"):
        formatted = subprocess.check_output(["gofmt"], input=lf, cwd=ROOT)
        if formatted != lf:
            bad_format.append(name)
        if formatted != data:
            raw_format_count += 1

manifest = {
    "captured_utc": datetime.now(timezone.utc).isoformat(),
    "branch": git("branch", "--show-current"), "base": "88097be5d9bb6996b718fd185515ca324629fda6",
    "head": git("rev-parse", "HEAD"), "source_state": "uncommitted local branch; hashes identify final tested source",
    "go": subprocess.check_output(["go", "version"]).decode().strip(),
    "files": source,
}
(HERE / "source-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
(HERE / "format-validation.log").write_text(
    f"LF-normalized repository Go source: {sum(n.endswith('.go') for n in source)} files, unformatted={len(bad_format)}\n"
    f"Raw working-tree differences caused by existing CRLF checkout: {raw_format_count}\n"
    f"Unformatted after LF normalization: {bad_format}\n", encoding="utf-8")
if bad_format:
    raise SystemExit("canonical gofmt check failed")

patch = subprocess.check_output(["git", "diff", "--no-ext-diff", "HEAD", "--", "internal", "runtime"], cwd=ROOT)
for name in git("ls-files", "--others", "--exclude-standard").splitlines():
    if name.startswith("internal/") and name.endswith(".go"):
        result = subprocess.run(["git", "diff", "--no-index", "--", "/dev/null", name], cwd=ROOT, stdout=subprocess.PIPE, check=False)
        if result.returncode not in (0, 1):
            raise SystemExit("untracked diff capture failed")
        patch += result.stdout
(HERE / "source-diff.patch").write_bytes(patch)

for stem in ("go-test", "final-go-test"):
    raw = HERE / (stem + ".jsonl")
    packed = HERE / (stem + ".jsonl.gz")
    if raw.exists():
        data = raw.read_bytes()
        packed.write_bytes(gzip.compress(data, mtime=0))
        raw.unlink()
    elif packed.exists():
        data = gzip.decompress(packed.read_bytes())
    else:
        continue
    events = [json.loads(line) for line in data.decode("utf-8-sig").splitlines() if line.startswith("{")]
    skipped = [e for e in events if e.get("Action") == "skip" and "Test" in e]
    summary = {
        "source": "source-manifest.json" if stem == "final-go-test" else "earlier source before close/late-channel fixes; historical only",
        "package_pass": sum(e.get("Action") == "pass" and "Test" not in e for e in events),
        "test_pass_including_subtests": sum(e.get("Action") == "pass" and "Test" in e for e in events),
        "failures": [e for e in events if e.get("Action") == "fail"],
        "skipped_tests": [{"package": e["Package"], "test": e["Test"], "reason": "".join(x.get("Output", "") for x in events if x.get("Test") == e["Test"] and x.get("Package") == e["Package"] and x.get("Action") == "output").strip()} for e in skipped],
    }
    (HERE / (stem + "-summary.json")).write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
print("source manifest, complete source diff, canonical formatting and existing Go logs captured")
