from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
import urllib.request
import hashlib
import json
import tarfile
import gzip
import io
import re

BASE = Path(__file__).resolve().parent
DOWNLOADS = BASE / "downloads"
DOWNLOADS.mkdir(exist_ok=True)
ORIGIN = "https://dl-cdn.alpinelinux.org/alpine/v3.23"
URLS = {
    "rootfs.tar.gz": ORIGIN + "/releases/x86_64/alpine-minirootfs-3.23.8-x86_64.tar.gz",
    "rootfs.sha256": ORIGIN + "/releases/x86_64/alpine-minirootfs-3.23.8-x86_64.tar.gz.sha256",
    "vmlinuz-virt": ORIGIN + "/releases/x86_64/netboot-3.23.8/vmlinuz-virt",
    "APKINDEX.tar.gz": ORIGIN + "/main/x86_64/APKINDEX.tar.gz",
}

def fetch(item):
    name, url = item
    path = DOWNLOADS / name
    if not path.exists():
        with urllib.request.urlopen(url, timeout=40) as response, path.with_suffix(path.suffix + ".part").open("wb") as output:
            while chunk := response.read(1024 * 1024):
                output.write(chunk)
        path.with_suffix(path.suffix + ".part").replace(path)
    return {"name": name, "url": url, "size": path.stat().st_size,
            "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}

with ThreadPoolExecutor(max_workers=4) as pool:
    records = list(pool.map(fetch, URLS.items()))
expected = (DOWNLOADS / "rootfs.sha256").read_text().split()[0].lower()
assert hashlib.sha256((DOWNLOADS / "rootfs.tar.gz").read_bytes()).hexdigest() == expected
with tarfile.open(DOWNLOADS / "APKINDEX.tar.gz", "r:gz") as archive:
    text = archive.extractfile("APKINDEX").read().decode()
packages = {}
provided = {}
for block in text.split("\n\n"):
    fields = dict(line.split(":", 1) for line in block.splitlines() if ":" in line)
    if "P" not in fields:
        continue
    packages[fields["P"]] = fields
    for alias in fields.get("p", "").split():
        provided.setdefault(alias.split("=")[0], fields["P"])
needed = set()
def need(name):
    if name.startswith("!"):
        return
    name = re.split(r"[<>=~]", name)[0]
    name = name if name in packages else provided.get(name, name)
    if name in needed:
        return
    if name not in packages:
        raise RuntimeError("unresolved dependency: " + name)
    needed.add(name)
    for dependency in packages[name].get("D", "").split():
        need(dependency)
for name in ("bash", "coreutils", "python3", "curl"):
    need(name)
apk_urls = [(name + "-" + packages[name]["V"] + ".apk", ORIGIN + "/main/x86_64/" + name + "-" + packages[name]["V"] + ".apk") for name in sorted(needed)]
with ThreadPoolExecutor(max_workers=4) as pool:
    records.extend(pool.map(fetch, apk_urls))
(BASE / "downloads-manifest.json").write_text(json.dumps({"rootfs_published_sha256_verified": True, "packages": sorted(needed), "files": records}, indent=2) + "\n")
print(json.dumps({"downloaded_files": len(records), "packages": sorted(needed), "rootfs_sha256_verified": True}))
