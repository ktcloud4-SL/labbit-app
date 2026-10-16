"""Read-only Git comparison and outside-checkout documentation apply check."""
import hashlib
import json
import pathlib
import subprocess

OUT = pathlib.Path(__file__).resolve().parent
REPO = OUT.parent / "labbit-connector-gates-20261015"
BASE = "0051d8d6803fd447f7b690dd895a0a928c3c7302"
LATEST = "27039422d48c39eb4edd41606fbc829156e43355"

def git(*args):
    return subprocess.check_output(["git", "-C", str(REPO), *args])

names = git("diff", "--name-only", BASE, LATEST).decode().splitlines()
wire_paths = git("ls-tree", "-r", "--name-only", BASE, "contracts/connector", "contracts/http", "contracts/realtime").decode().splitlines()
wire_paths = [p for p in wire_paths if p.endswith((".json", ".yaml"))]
wire_paths += ["go.mod", "go.sum"]
wire = []
for path in wire_paths:
    a, b = git("show", f"{BASE}:{path}"), git("show", f"{LATEST}:{path}")
    wire.append({"path": path, "same": a == b, "base_sha256": hashlib.sha256(a).hexdigest(), "latest_sha256": hashlib.sha256(b).hexdigest()})

patch_paths = ["contracts/connector/README.md", "runtime/contract.yaml"]
patch = git("diff", "--binary", "--", *patch_paths)
check_dir = OUT / "latest-main-document-check"
for path in patch_paths:
    target = check_dir / path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(git("show", f"{LATEST}:{path}"))
patch_path = OUT / "current-routing-contracts.patch"
patch_path.write_bytes(patch)
applied = subprocess.run(["git", "apply", "--check", str(patch_path)], cwd=check_dir, capture_output=True, text=True)
result = {
    "base": BASE, "latest_fetched_sha": LATEST,
    "checkout_head": git("rev-parse", "HEAD").decode().strip(),
    "origin_main": git("rev-parse", "origin/main").decode().strip(),
    "changed_file_count": len(names), "changed_files": names,
    "wire_and_module_identity": wire,
    "wire_and_module_differences": sum(not x["same"] for x in wire),
    "connector_or_preview_product_changes": [p for p in names if p.startswith(("internal/connector/", "internal/server/preview/", "internal/server/previewsession/", "internal/server/connector/"))],
    "current_contract_patch_apply_check_on_latest": {"exit_code": applied.returncode, "stdout": applied.stdout, "stderr": applied.stderr, "external_tree": str(check_dir)},
    "new_migration": "db/migrations/000008_operation_dispatch_deferral.sql",
    "readiness_boundary": "Latest PROVISION stores Provider success/resources but leaves LabInstance PROVISIONING. Existing PreviewSession authority requires READY. Fresh production Provision-to-Preview is not proven by this local MockGateway fixture.",
    "integration_execution": "NOT RUN: no merged checkout/build, migration8 DB, real worker dispatch, or latest-main integrated lifecycle was executed by reviewer.",
    "shared_checkout_mutations": 0,
}
assert result["checkout_head"] == BASE
assert result["wire_and_module_differences"] == 0
assert not result["connector_or_preview_product_changes"]
assert applied.returncode == 0, applied.stderr
(OUT / "latest-main-impact.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(json.dumps({"changed_files": len(names), "wire_module_inputs": len(wire), "wire_module_differences": 0, "contract_patch_apply_check": applied.returncode, "integrated_execution": "NOT RUN"}))
