from pathlib import Path
from collections import Counter
from datetime import datetime, timezone
import hashlib, json, re, shutil, subprocess
BASE = Path(__file__).resolve().parent
APP = Path(r"C:\Users\user\Documents\ChatGPT\3차 2\tmp\labbit-dr-stage3-offline-20261121")
OUT = APP / "evidence/LBT-60/2026-11-21-linux-offline"

def write(name, value):
    (OUT / name).write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
def digest(path):
    return {"path": path.name, "size": path.stat().st_size, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}
def section(raw, name):
    start = "@@BEGIN_" + name + "@@"
    match = re.search(r"@@END_" + name + r":([0-9]+)@@", raw)
    assert start in raw and match, "incomplete section: " + name
    return raw[raw.index(start)+len(start):match.start()].strip() + "\n", int(match.group(1))
def summarize(raw):
    events = [json.loads(line) for line in raw.splitlines() if line.startswith('{')]
    totals = Counter(e["Action"] for e in events if e.get("Test") and e.get("Action") in ("pass", "fail", "skip"))
    return {"test_events": {k: totals[k] for k in ("pass", "fail", "skip")},
            "skips": [e["Test"] for e in events if e.get("Test") and e.get("Action") == "skip"],
            "failures": [e.get("Test", "<package>") for e in events if e.get("Action") == "fail"]}
raw1 = (OUT / "qemu-attempt01.log").read_text(encoding="utf-8-sig")
raw2 = (OUT / "qemu-attempt02.log").read_text(encoding="utf-8-sig")
assert "@@GUEST_RESULTS:0:1:0:0@@" in raw1 and "@@GUEST_RESULTS:0:0:0@@" in raw2
assert "reboot: Power down" in raw1 and "reboot: Power down" in raw2
result = {}
for source, name, output in ((raw1, "OPENSTACK", "go-openstack.jsonl"),
                             (raw1, "INSTALLER", "go-installer-initial.jsonl"),
                             (raw2, "INSTALLER", "go-installer-final.jsonl"),
                             (raw2, "WORKSPACEFILE", "go-workspacefile.jsonl"),
                             (raw2, "CREDENTIAL", "go-credential.jsonl")):
    contents, code = section(source, name)
    lines = "\n".join(line for line in contents.splitlines() if line.startswith('{')) + "\n"
    (OUT / output).write_text(lines, encoding="utf-8")
    result[output] = {"exit_code": code, **summarize(lines)}
for name, output in (("MANAGEMENT_ROUTE", "python-normal.log"), ("MANAGEMENT_ROUTE_OPTIMIZED", "python-optimized.log")):
    contents, code = section(raw1, name)
    assert code == 0 and "Ran 10 tests" in contents and contents.rstrip().endswith("OK")
    (OUT / output).write_text(contents, encoding="utf-8")
    result[output] = {"exit_code": code, "tests": 10, "pass": 10, "fail": 0, "skip": 0}

before = json.loads((APP / "evidence/LBT-60/2026-11-21-regression-diagnosis/source-manifest.json").read_text(encoding="utf-8"))
assert all((APP / item["path"]).stat().st_size == item["size"] and
           hashlib.sha256((APP / item["path"]).read_bytes()).hexdigest() == item["sha256"] for item in before["files"])
write("source-comparison.json", {"base_head": before["head"], "branch": before["branch"],
    "current_head": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=APP).decode().strip(),
    "previous_snapshot": "../2026-11-21-regression-diagnosis/source-manifest.json", "checked_files": len(before["files"]), "mismatches": []})
for name in ("payload-manifest-attempt01.json", "payload-manifest.json", "downloads-manifest-attempt01.json", "downloads-manifest.json"):
    shutil.copyfile(BASE / name, OUT / name)
helpers = OUT / "harness"
helpers.mkdir(exist_ok=True)
for name in ("prepare_linux.py", "build_image_attempt01.py", "build_image.py", "init-attempt01.sh", "init.sh", "finalize_evidence.py"):
    shutil.copyfile(BASE / name, helpers / name)
write("harness-manifest.json", {"files": [digest(p) for p in sorted(helpers.iterdir()) if p.is_file()]})
qemu = Path(r"C:\Program Files\GNS3\qemu-3.1.0\qemu-system-x86_64.exe")
kernel = BASE / "downloads/vmlinuz-virt"
write("environment.json", {"created_at_utc": datetime.now(timezone.utc).isoformat(),
    "qemu": {**digest(qemu), "version": "2.11.0 (v2.11.0-11693-g21057c841e-dirty)", "installed_directory_name": "qemu-3.1.0"},
    "guest": {"alpine": "3.23.8", "kernel": "6.18.60-0-virt", "bash": "5.3.3", "python": "3.12.16", "coreutils": "9.8", "filesystem": "RAM rootfs + tmpfs /tmp; Linux fsync API exercised, no disk crash durability proof"},
    "kernel": digest(kernel), "rootfs_published_sha256_verified": True, "kernel_apk_publisher_signature_verified": False,
    "qemu_args": ["-nodefaults", "-no-user-config", "-display", "none", "-monitor", "none", "-serial", "stdio", "-no-reboot", "-m", "1536", "-smp", "2", "-accel", "tcg", "-net", "none", "-kernel", "<vmlinuz-virt>", "-initrd", "<image>", "-append", "console=ttyS0 rdinit=/init panic=1"],
    "initramfs_attempt01_sha256": (BASE / "initramfs-attempt01.sha256").read_text(encoding="utf-8-sig").strip().lower(),
    "initramfs_attempt02": digest(BASE / "test-initramfs.gz"),
    "build": {"go": "1.27.1", "GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0", "race": False},
    "qemu_exit_codes": {"attempt01": 0, "attempt02": 0}, "guest_network_interfaces": ["lo"],
    "host_shared_directories": [], "guest_disks": [], "host_credentials_in_guest": False,
    "cleanup": "Both guests powered down; ephemeral RAM filesystems discarded. Host scratch files retained for reproducibility; no installed services/WSL/registry changes."
})
write("test-summary.json", {"date_kst": "2026-11-21", "results": result,
    "actual_aws_calls": False, "shared_database_access": False, "push_merge_deploy": False, "external_messages": False,
    "execution_gate": "HOLD", "recovery_authorized": False, "cloud_verified": False, "replica_lag": "FAIL",
    "not_verified": ["REQUIRED_SETTINGS", "RUNTIME_PROVENANCE", "SERVICE_FUNCTIONS", "SINGLE_WRITER"],
    "scope": "Actual Linux kernel execution in local isolated QEMU; Go fixtures and Linux filesystem behavior, not real cloud/production Ubuntu/systemd/race/DR E2E.",
    "counting": "Go Test terminal events include parent/subtest events; do not aggregate repeated installer attempts as unique tests."})
write("artifact-manifest.json", {"files": [digest(p) for p in sorted(OUT.iterdir()) if p.is_file() and p.name != "artifact-manifest.json"]})
print(json.dumps({k: v.get("test_events", v.get("tests")) for k,v in result.items()}))
