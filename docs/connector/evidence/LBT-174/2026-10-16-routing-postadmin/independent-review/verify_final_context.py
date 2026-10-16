"""Final read-only SSOT/main/source comparison; no checkout or remote writes."""
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import subprocess
import sys

OUT = Path(__file__).resolve().parent
REPO = OUT.parent / "labbit-connector-gates-20261015"
STAGE = OUT.parent / "lbt174-routing-postadmin-20261016/final-verified"
def read(path): return json.loads(path.read_text(encoding="utf-8-sig"))
def digest(value): return hashlib.sha256(json.dumps(value, sort_keys=True, ensure_ascii=False).encode()).hexdigest()
def git(*args): return subprocess.check_output(["git", *args], cwd=REPO).decode("utf-8").strip()
closing = "--closing" in sys.argv
previous, final = read(OUT / "ssot-refresh.json"), read(OUT / ("ssot-closing-refresh.json" if closing else "ssot-final-refresh.json"))
reviewed_main = "1e2b9c20fa2cfd7d7d449c8abba8558b5a614df4" if closing else "ee05485bd9a1668f222e7a54fb5f880ea197e35a"
pages_before = {p["id"]: p for p in previous["pages"]}
pages = [{"id": p["id"], "title": p["title"], "version": p["version"]["number"],
          "sameBody": p.get("body") == pages_before[p["id"]].get("body"),
          "sameVersion": p["version"]["number"] == pages_before[p["id"]]["version"]["number"],
          "bodySHA256": digest(p.get("body"))} for p in final["pages"]]
manifest = read(STAGE / "source-manifest.json")
source_errors = [p for p,h in manifest["files"].items() if hashlib.sha256((REPO/p).read_bytes().replace(b"\r\n", b"\n")).hexdigest() != h]
remote = git("ls-remote", "origin", "refs/heads/main").split()[0]
comments = final["jira"]["fields"].get("comment", {})
count = comments.get("total", len(comments.get("comments", []))) if isinstance(comments, dict) else len(comments)
report = {"fetchedUTC": final["fetchedAt"], "observedUTC": datetime.now(timezone.utc).isoformat(),
          "jiraUpdated": final["jira"]["fields"]["updated"], "jiraCommentCount": count,
          "jiraDescriptionSame": final["jira"]["fields"]["description"] == previous["jira"]["fields"]["description"],
          "pages": pages, "remoteMain": remote, "reviewedIntegratedMain": reviewed_main, "mainChangedFromReviewedIntegration": remote != reviewed_main,
          "primaryHEAD": git("rev-parse", "HEAD"), "primaryOriginMainRef": git("rev-parse", "origin/main"),
          "primaryStagedFiles": git("diff", "--cached", "--name-only").splitlines(),
          "sourceFiles": len(manifest["files"]), "sourceMismatch": source_errors,
          "sourceSHA256": "7e8b2cac4e25d20bbb3d44b3a749cfcd0fe350689bf125fda18202bcbd722e31",
          "scope": "Jira/Confluence GET and git ls-remote only; no fetch/ref updates, product edits, cloud changes or remote writes"}
assert report["jiraDescriptionSame"] and count == 0
assert all(p["sameBody"] and p["sameVersion"] for p in pages)
assert not source_errors and len(manifest["files"]) == 521
assert not report["primaryStagedFiles"]
assert report["primaryHEAD"] == report["primaryOriginMainRef"] == "0051d8d6803fd447f7b690dd895a0a928c3c7302"
(OUT / ("closing-context-verification.json" if closing else "final-context-verification.json")).write_text(json.dumps(report, ensure_ascii=False, indent=2)+"\n", encoding="utf-8")
print(json.dumps({"SSOTSame": True, "remoteMain": remote, "mainChanged": report["mainChangedFromReviewedIntegration"], "sourceMismatch": len(source_errors), "staged": len(report["primaryStagedFiles"])}))
