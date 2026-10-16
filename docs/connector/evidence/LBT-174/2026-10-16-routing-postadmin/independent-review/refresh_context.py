"""Read-only context refresh for post-administrator actual acceptance."""
import hashlib
import json
import pathlib
import subprocess

OUT = pathlib.Path(__file__).resolve().parent
RUN = OUT.parent / "lbt174-routing-postadmin-20261016"
OLD = OUT.parent / "lbt174-routing-fix-independent-20261016"
REPO = OUT.parent / "labbit-connector-gates-20261015"
BASE = "0051d8d6803fd447f7b690dd895a0a928c3c7302"
PREVIOUS_MAIN = "27039422d48c39eb4edd41606fbc829156e43355"
LATEST = "ee05485bd9a1668f222e7a54fb5f880ea197e35a"
def sha(data): return hashlib.sha256(data).hexdigest()
def read(path): return json.loads(path.read_text(encoding="utf-8-sig"))
def git(*args): return subprocess.check_output(["git", "-C", str(REPO), *args])
ssot = read(OUT / "ssot-refresh.json")
old_ssot = read(OLD / "ssot-refresh.json")
old_pages = {p["id"]: p for p in old_ssot["pages"]}
def body(page): return json.dumps(page.get("body"), sort_keys=True, ensure_ascii=False).encode()
pages = [{"id": p["id"], "title": p["title"], "version": p["version"]["number"],
          "sameVersionAsPrevious": p["version"]["number"] == old_pages[p["id"]]["version"]["number"],
          "sameBodyAsPrevious": body(p) == body(old_pages[p["id"]]), "bodySHA256": sha(body(p))} for p in ssot["pages"]]
comments = ssot["jira"]["fields"].get("comment", {})
comment_count = len(comments.get("comments", [])) if isinstance(comments, dict) else len(comments)
manifest = read(RUN / "source-manifest.json")
files = {p: sha((REPO / p).read_bytes().replace(b"\r\n", b"\n")) for p in manifest["files"]}
aggregate = sha(json.dumps(files, sort_keys=True).encode())
source_errors = [p for p in files if files[p] != manifest["files"][p]]
previous_identity = read(OLD / "source-current-identity.json")

old_package = REPO / "docs/connector/evidence/LBT-174/2026-10-16-routing-fix"
seal = read(old_package / "package-checksums.json")["files"]
seal_errors = [p for p, h in seal.items() if not (old_package / p).is_file() or sha((old_package / p).read_bytes()) != h]
wire_paths = [p for p in git("ls-tree", "-r", "--name-only", BASE, "contracts").decode().splitlines() if p.endswith((".json", ".yaml"))]
wire_paths += ["go.mod", "go.sum"]
wire_changed = [p for p in wire_paths if git("show", BASE + ":" + p) != git("show", LATEST + ":" + p)]
main_paths = git("diff", "--name-only", PREVIOUS_MAIN, LATEST).decode().splitlines()
all_main_paths = git("diff", "--name-only", BASE, LATEST).decode().splitlines()
changed = sorted(set(git("diff", "--name-only", "HEAD", "--", "internal", "contracts", "runtime").decode().splitlines()
                     + git("ls-files", "--others", "--exclude-standard", "--", "internal", "contracts", "runtime").decode().splitlines()))
result = {
    "refreshedUTC": ssot["fetchedAt"],
    "jira": {"updated": ssot["jira"]["fields"]["updated"], "commentCount": comment_count,
             "sameDescriptionAsPrevious": ssot["jira"]["fields"]["description"] == old_ssot["jira"]["fields"]["description"]},
    "pages": pages,
    "base": BASE, "checkoutHEAD": git("rev-parse", "HEAD").decode().strip(),
    "branch": git("branch", "--show-current").decode().strip(), "originMain": git("rev-parse", "origin/main").decode().strip(),
    "sourceInputs": len(files), "sourceSHA256": aggregate, "sourceErrors": source_errors,
    "sameFullSourceAsPreviousVerified": aggregate == previous_identity["sourceSHA256"],
    "changedCodeContractFiles": len(changed), "changedPaths": changed,
    "stagedPaths": git("diff", "--cached", "--name-only").decode().splitlines(),
    "previousRoutingPackageSealedFiles": len(seal), "previousRoutingPackageSealErrors": seal_errors,
    "latestFetchedMain": LATEST, "mainAddedSincePreviousReview": main_paths, "mainChangesAgainstBase": all_main_paths,
    "wireModuleInputs": len(wire_paths), "wireModuleChangesAgainstBase": wire_changed,
    "newConsumerBoundary": "Latest upstream now dispatches RESET/CLEANUP using existing wire and closes SaaS Terminal/Preview via CloseForLabMutation. It adds migration9 on top of migration8. Current0051 execution does not validate that integrated SaaS worker path or latest frontend polling/tests.",
    "saaSReadinessBoundary": "Provider SUCCEEDED remains distinct from READY; current actual Connector+MockGateway does not prove SaaS PROVISION-to-Preview readiness.",
    "reviewerMutations": "Only this new external folder; no product, previous Evidence, cloud, service, remote writes or checkout/ref updates. SHA fetch used --no-write-fetch-head.",
}
assert result["checkoutHEAD"] == BASE and result["originMain"] == BASE
assert len(files) == 520 and result["sameFullSourceAsPreviousVerified"] and not source_errors
assert len(changed) == 37 and not result["stagedPaths"] and not seal_errors and not wire_changed
assert result["jira"]["sameDescriptionAsPrevious"] and all(p["sameBodyAsPrevious"] and p["sameVersionAsPrevious"] for p in pages)
(OUT / "context-refresh-check.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(json.dumps({"source": len(files), "sourceSame": result["sameFullSourceAsPreviousVerified"], "changedFiles": len(changed), "previousSealed": len(seal), "latestMain": LATEST, "newMainPaths": len(main_paths), "wireChanges": wire_changed, "ssotBodiesUnchanged": True}))
