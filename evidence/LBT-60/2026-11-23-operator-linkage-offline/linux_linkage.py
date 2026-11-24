from pathlib import Path, PurePosixPath
import gzip, io, tarfile, stat, hashlib, json, subprocess

APP = Path(__file__).resolve().parent.parent
BASE = APP / '.local'
CACHE = Path(r'C:\Users\user\.codex\tmp\stage3-linux-20261121')
manifest = json.loads((CACHE / 'downloads-manifest.json').read_text())
for f in manifest['files']:
    raw = (CACHE / 'downloads' / f['name']).read_bytes()
    assert len(raw) == f['size'] and hashlib.sha256(raw).hexdigest() == f['sha256']
entries = {}
def add_tar(path):
    with tarfile.open(fileobj=io.BytesIO(gzip.decompress(path.read_bytes())), mode='r:', ignore_zeros=True) as archive:
        for info in archive:
            name = info.name.removeprefix('./').rstrip('/')
            if not name or name.startswith(('.', '/')) or '/../' in '/' + name + '/': continue
            if info.isdir(): kind, content = stat.S_IFDIR, b''
            elif info.issym(): kind, content = stat.S_IFLNK, info.linkname.encode()
            elif info.isreg(): kind, content = stat.S_IFREG, archive.extractfile(info).read()
            elif info.islnk():
                entries[name] = entries[info.linkname.removeprefix('./')]
                continue
            else: continue
            entries[name] = (kind | info.mode, content)
add_tar(CACHE / 'downloads/rootfs.tar.gz')
for p in sorted((CACHE / 'downloads').glob('*.apk')): add_tar(p)
def put(name, content, mode=0o755):
    parts = PurePosixPath(name).parts
    for i in range(1, len(parts)): entries.setdefault('/'.join(parts[:i]), (stat.S_IFDIR | 0o755, b''))
    entries[name] = (stat.S_IFREG | mode, content)
binary = BASE / 'linkage-drcontrol-linux.test'
assert binary.read_bytes()[:4] == b'\x7fELF'
put('payload/drcontrol.test', binary.read_bytes())
put('payload/test2json', (CACHE / 'test2json').read_bytes())
init = b'''#!/bin/sh
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export HOME=/root TMPDIR=/tmp LANG=C.UTF-8
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev
mkdir -p /tmp /run
mount -t tmpfs -o mode=1777,nosuid,nodev tmpfs /tmp
ip link set lo up
echo '@@GUEST_READY@@'
uname -a
ls -l /sys/class/net
sha256sum /payload/drcontrol.test /payload/test2json
/payload/test2json -p github.com/ktcloud4-SL/labbit-app/internal/drcontrol -t /payload/drcontrol.test -test.v=test2json -test.timeout=90s
result=$?
echo "@@LINKAGE_RESULT:$result@@"
sync
poweroff -f
'''
put('init', init)
(BASE / 'linkage-linux-init.sh').write_bytes(init)
def emit(out, name, mode, content, ino):
    encoded = name.encode() + b'\0'
    fields = [ino, mode, 0, 0, 2 if stat.S_ISDIR(mode) else 1, 0, len(content), 0, 0, 0, 0, len(encoded), 0]
    header = b'070701' + b''.join(f'{v:08x}'.encode() for v in fields)
    out.write(header + encoded + b'\0' * (-(len(header) + len(encoded)) % 4))
    out.write(content + b'\0' * (-len(content) % 4))
image = BASE / 'linkage-linux-initramfs.gz'
with gzip.GzipFile(filename=str(image), mode='wb', compresslevel=3, mtime=0) as out:
    for ino, (name, (mode, content)) in enumerate(sorted(entries.items(), key=lambda x:(len(PurePosixPath(x[0]).parts),x[0])), 1): emit(out, name, mode, content, ino)
    emit(out, 'TRAILER!!!', 0, b'', len(entries)+1)
cmd = [r'C:\Program Files\GNS3\qemu-3.1.0\qemu-system-x86_64.exe', '-accel', 'tcg', '-m', '768M', '-display', 'none', '-monitor', 'none', '-serial', 'file:' + str(BASE / 'linkage-linux-serial.log'), '-no-reboot', '-net', 'none', '-kernel', str(CACHE / 'downloads/vmlinuz-virt'), '-initrd', str(image), '-append', 'console=ttyS0 quiet panic=-1']
(BASE / 'linkage-linux-environment.json').write_text(json.dumps({'command':cmd,'network':'none; loopback only','disks':[],'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'image_sha256':hashlib.sha256(image.read_bytes()).hexdigest(),'cache_checksums_verified':True}, indent=2))
with (BASE / 'linkage-linux-qemu.log').open('w') as out: subprocess.run(cmd, stdout=out,stderr=subprocess.STDOUT,check=True,timeout=150)
text = (BASE / 'linkage-linux-serial.log').read_text(errors='replace')
assert '@@LINKAGE_RESULT:0@@' in text, text[-3000:]
print('Linux no-NIC drcontrol tests PASS')
