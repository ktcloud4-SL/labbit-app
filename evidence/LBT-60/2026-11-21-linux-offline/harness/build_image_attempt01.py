from pathlib import Path, PurePosixPath
import io, tarfile, gzip, json, hashlib, stat, subprocess, time
BASE = Path(__file__).resolve().parent
APP = Path(r"C:\Users\user\Documents\ChatGPT\3차 2\tmp\labbit-dr-stage3-offline-20261121")
entries = {}

def add_tar(path, apk=False):
    raw = gzip.decompress(path.read_bytes())
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:", ignore_zeros=True) as archive:
        for info in archive:
            name = info.name.removeprefix("./").rstrip("/")
            if not name or name.startswith(".") or "/../" in "/" + name + "/" or name.startswith("/"):
                continue
            if info.isdir(): kind, content = stat.S_IFDIR, b""
            elif info.issym(): kind, content = stat.S_IFLNK, info.linkname.encode()
            elif info.isreg(): kind, content = stat.S_IFREG, archive.extractfile(info).read()
            elif info.islnk():
                target = entries.get(info.linkname.removeprefix("./"))
                if target is None: raise RuntimeError("unresolved hardlink " + name)
                entries[name] = target
                continue
            else: continue
            entries[name] = (kind | info.mode, content)

add_tar(BASE / "downloads/rootfs.tar.gz")
for path in sorted((BASE / "downloads").glob("*.apk")):
    add_tar(path, True)

def put(name, data, mode=0o644):
    parts = PurePosixPath(name).parts
    for i in range(1, len(parts)):
        parent = "/".join(parts[:i])
        entries.setdefault(parent, (stat.S_IFDIR | 0o755, b""))
    entries[name] = (stat.S_IFREG | mode, data)

names = subprocess.check_output(["git", "ls-files", "-c", "-o", "--exclude-standard", "-z"], cwd=APP).decode().split("\0")
source = []
for name in sorted(set(names) - {""}):
    if not (name.startswith("internal/connector/provider/openstack/") or name.startswith("internal/connector/installer/")
            or name in ("scripts/installer/install.sh", "scripts/installer/install_test.sh", "runtime/contract.yaml")):
        continue
    path = APP / name
    if not path.is_file(): continue
    raw = path.read_bytes()
    put("app/" + name, raw)
    source.append({"path": name, "size": len(raw), "sha256": hashlib.sha256(raw).hexdigest()})
binary = []
for name in ("openstack.test", "installer.test", "test2json"):
    data = (BASE / name).read_bytes()
    assert data[:4] == b"\x7fELF"
    put("payload/" + name, data, 0o755)
    binary.append({"path": name, "size": len(data), "sha256": hashlib.sha256(data).hexdigest()})
manifest = {"head": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=APP).decode().strip(),
            "uncommitted": True, "source": source, "binaries": binary,
            "build": {"go": "1.27.1", "GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0", "race": False}}
manifest_bytes = (json.dumps(manifest, indent=2) + "\n").encode()
(BASE / "payload-manifest.json").write_bytes(manifest_bytes)
put("payload/manifest.json", manifest_bytes)
init = r'''#!/bin/sh
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export HOME=/root TMPDIR=/tmp LANG=C.UTF-8
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev
mkdir -p /dev/pts /tmp /run
mount -t devpts devpts /dev/pts
mount -t tmpfs -o mode=1777,nosuid,nodev tmpfs /tmp
ip link set lo up
echo '@@GUEST_READY@@'
uname -a
date -u '+%Y-%m-%dT%H:%M:%SZ'
cat /etc/alpine-release
ls -l /sys/class/net
bash --version | head -1
python3 --version
sha256sum --version | head -1
sha256sum /payload/manifest.json /payload/openstack.test /payload/installer.test /payload/test2json
echo '@@BEGIN_OPENSTACK@@'
cd /app/internal/connector/provider/openstack
/payload/test2json -p github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack -t /payload/openstack.test -test.v=test2json -test.timeout=180s
os_result=$?
echo "@@END_OPENSTACK:$os_result@@"
echo '@@BEGIN_INSTALLER@@'
cd /app/internal/connector/installer
/payload/test2json -p github.com/ktcloud4-SL/labbit-app/internal/connector/installer -t /payload/installer.test -test.v=test2json -test.timeout=180s
installer_result=$?
echo "@@END_INSTALLER:$installer_result@@"
cd /app
echo '@@BEGIN_MANAGEMENT_ROUTE@@'
python3 -m unittest discover -s internal/connector/provider/openstack -p ktcloud_management_route_guest_test.py -v
py_result=$?
echo "@@END_MANAGEMENT_ROUTE:$py_result@@"
echo '@@BEGIN_MANAGEMENT_ROUTE_OPTIMIZED@@'
python3 -O -m unittest discover -s internal/connector/provider/openstack -p ktcloud_management_route_guest_test.py -v
py_opt_result=$?
echo "@@END_MANAGEMENT_ROUTE_OPTIMIZED:$py_opt_result@@"
echo "@@GUEST_RESULTS:$os_result:$installer_result:$py_result:$py_opt_result@@"
sync
poweroff -f
'''
put("init", init.encode(), 0o755)
(BASE / "init.sh").write_text(init, encoding="utf-8", newline="\n")

def emit(file, name, mode, content, ino):
    encoded = name.encode() + b"\0"
    fields = [ino, mode, 0, 0, 2 if stat.S_ISDIR(mode) else 1, 0, len(content), 0, 0, 0, 0, len(encoded), 0]
    header = b"070701" + b"".join(f"{value:08x}".encode() for value in fields)
    file.write(header + encoded)
    file.write(b"\0" * (-(len(header) + len(encoded)) % 4))
    file.write(content)
    file.write(b"\0" * (-len(content) % 4))

with gzip.GzipFile(filename=str(BASE / "test-initramfs.gz"), mode="wb", compresslevel=3, mtime=0) as output:
    for ino, (name, (mode, content)) in enumerate(sorted(entries.items(), key=lambda x: (len(PurePosixPath(x[0]).parts), x[0])), 1):
        emit(output, name, mode, content, ino)
    emit(output, "TRAILER!!!", 0, b"", len(entries) + 1)
print(json.dumps({"entries": len(entries), "source_files": len(source), "initramfs_bytes": (BASE / "test-initramfs.gz").stat().st_size,
                  "payload_manifest_sha256": hashlib.sha256(manifest_bytes).hexdigest()}))
