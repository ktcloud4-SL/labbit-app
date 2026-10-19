"""Independent immutable snapshot checks. Never modifies package/checkout."""
import gzip
import hashlib
import io
import json
import pathlib
import re
import subprocess
import urllib.parse

OUT = pathlib.Path(__file__).resolve().parent
REPO = OUT.parent / "labbit-connector-gates-20261015"
PACKAGE = REPO / "docs/connector/evidence/LBT-174/2026-10-16-routing-fix"
BASE = "0051d8d6803fd447f7b690dd895a0a928c3c7302"

def sha(data): return hashlib.sha256(data).hexdigest()
def read_json(path): return json.loads(path.read_text(encoding="utf-8-sig"))
def export_links(raw):
    def replace(match):
        target = match[2]
        if "://" in target or target.startswith("#"): return match[0]
        base, sep, anchor = target.partition("#")
        if base.endswith(".go"): base += ".txt"
        elif base.endswith(".log"): base += ".gz"
        return match[1] + base + (sep + anchor if sep else "") + ")"
    return re.sub(r"(\[[^\]]+\]\()([^\)\n]+)\)", replace, raw.decode("utf-8")).encode("utf-8")

raw_errors, transforms, gzip_count = [], 0, 0
provenance = read_json(PACKAGE / "raw-provenance.json")
for row in provenance:
    original = OUT.parent / row["original"]
    artifact = PACKAGE / row["artifact"]
    raw = original.read_bytes()
    packed = artifact.read_bytes()
    decoded = gzip.decompress(packed) if row["compression"] == "gzip" else packed
    gzip_count += row["compression"] == "gzip"
    expected = export_links(raw) if row["transformation"] else raw
    if row["transformation"]: transforms += 1
    if len(raw) != row["rawBytes"] or sha(raw) != row["rawSHA256"]:
        raw_errors.append({"artifact": row["artifact"], "reason": "original differs from snapshot"})
    if len(decoded) != row["artifactUncompressedBytes"] or sha(decoded) != row["artifactUncompressedSHA256"] or decoded != expected:
        raw_errors.append({"artifact": row["artifact"], "reason": "artifact bytes/transform mismatch"})
    if row["compression"] == "gzip" and packed[4:8] != bytes(4):
        raw_errors.append({"artifact": row["artifact"], "reason": "gzip mtime not deterministic0"})

inventory = read_json(PACKAGE / "code-inventory.json")
manifest = read_json(PACKAGE / "run/source-manifest.json")
paths = list(manifest["files"])
request = b"".join((BASE + ":" + path + "\n").encode() for path in paths)
proc = subprocess.run(["git", "-C", str(REPO), "cat-file", "--batch"], input=request, capture_output=True, check=True)
stream = io.BytesIO(proc.stdout)
restored, absent_base = {}, []
for path in paths:
    header = stream.readline().rstrip(b"\n")
    if header.endswith(b" missing"):
        absent_base.append(path)
        continue
    size = int(header.rsplit(b" ", 1)[1])
    restored[path] = stream.read(size)
    assert stream.read(1) == b"\n"

inventory_errors = []
for row in inventory:
    data = (PACKAGE / row["artifact"]).read_bytes()
    if sha(data) != row["rawSHA256"] or data != (REPO / row["path"]).read_bytes(): inventory_errors.append(row["path"])
    restored[row["path"]] = data
source_errors = [path for path in paths if path not in restored or sha(restored[path].replace(b"\r\n", b"\n")) != manifest["files"][path]]
context_errors = []
for row in read_json(PACKAGE / "context-inventory.json"):
    data = (PACKAGE / row["artifact"]).read_bytes()
    if sha(data) != row["rawSHA256"] or data != (REPO / row["path"]).read_bytes(): context_errors.append(row["path"])

frozen = []
for name in ["2026-10-16-followup", "2026-10-16-network-race"]:
    folder = PACKAGE.parent / name
    seal = read_json(folder / "package-checksums.json")
    # Both historical seals map relative file names directly to SHA256.
    entries = seal["files"] if "files" in seal else seal
    errors = [p for p, expected in entries.items() if not (folder / p).is_file() or sha((folder / p).read_bytes()) != expected]
    frozen.append({"folder": name, "files": len(entries), "errors": errors})

link_errors, pattern_matches = [], []
files = [p for p in PACKAGE.rglob("*") if p.is_file()]
current_seal = read_json(PACKAGE / "package-checksums.json")["files"]
seal_errors = [p for p, expected in current_seal.items() if not (PACKAGE / p).is_file() or sha((PACKAGE / p).read_bytes()) != expected]
unsealed_files = sorted(p.relative_to(PACKAGE).as_posix() for p in files
                        if p.relative_to(PACKAGE).as_posix() not in current_seal and p.name != "package-checksums.json")
patterns = [rb"-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----", rb"(?i)(?:password|access_token|refresh_token|auth_token)\s*[:=]\s*[\"'](?!\s*[\"'])[^\"']{8,}[\"']"]
for path in files:
    data = gzip.decompress(path.read_bytes()) if path.suffix == ".gz" else path.read_bytes()
    for index, pattern in enumerate(patterns):
        if re.search(pattern, data): pattern_matches.append({"path": path.relative_to(PACKAGE).as_posix(), "patternIndex": index})
    if path.suffix != ".md": continue
    for match in re.finditer(r"\[[^\]]+\]\(([^\)\n]+)\)", data.decode("utf-8")):
        target = match[1]
        if "://" in target or target.startswith("#") or target.startswith("/"): continue
        relative = urllib.parse.unquote(target.split("#", 1)[0].strip("<>"))
        if relative and not (path.parent / relative).exists(): link_errors.append({"path": str(path.relative_to(PACKAGE)), "target": target})

result = {"rawArtifactsReviewed": len(provenance), "gzipArtifacts": gzip_count, "markdownTransforms": transforms,
          "rawErrors": raw_errors, "changedSourceFiles": len(inventory), "inventoryErrors": inventory_errors,
          "sourceReconstructedFromBasePlusExports": len(paths), "sourceErrors": source_errors,
          "baseAbsentNewInputs": absent_base, "contextErrors": context_errors,
          "frozenHistoricalPackages": frozen, "filesScreened": len(files), "heuristicCandidates": pattern_matches,
          "exactProtectedValueScreen": "Root reports7 values/no matches; reviewer inspected metadata and independently repeats only heuristic screen here.",
          "localMarkdownLinkErrors": link_errors, "goSourcesInEvidence": len(list(PACKAGE.rglob("*.go"))),
          "currentSealFiles": len(current_seal), "currentSealErrors": seal_errors, "currentUnsealedFiles": unsealed_files,
          "packageResealedOrModifiedByReviewer": False, "boundary": str(len(provenance)) + "-artifact snapshot; later reviewer-final additions require re-copy and hash/seal by root."}
(OUT / "package-independent-check.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(json.dumps({"raw": len(provenance), "gzip": gzip_count, "changed": len(inventory), "source": len(paths),
                  "rawErrors": len(raw_errors), "inventoryErrors": len(inventory_errors), "sourceErrors": len(source_errors),
                  "frozen": sum(x["files"] for x in frozen), "frozenErrors": sum(len(x["errors"]) for x in frozen),
                  "sealFiles": len(current_seal), "sealErrors": seal_errors, "unsealedFiles": unsealed_files,
                  "heuristicCandidates": pattern_matches, "linkErrors": link_errors}))
assert not raw_errors and not inventory_errors and not source_errors and not context_errors
assert all(not x["errors"] for x in frozen)
assert not pattern_matches and not link_errors
assert not seal_errors and not unsealed_files
assert result["goSourcesInEvidence"] == 0
