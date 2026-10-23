"""Private SSH bootstrap. Payload arrives on stdin; never downloads from the VM."""
import contextlib
import fcntl
import hashlib
import os
import pathlib
import platform
import pwd
import secrets
import selectors
import signal
import stat
import struct
import subprocess
import sys
import time


def main():
    def timed_out(signum, frame):
        raise TimeoutError("Workspace preparation timed out")

    signal.signal(signal.SIGALRM, timed_out)
    signal.alarm(120)
    root, helper, username, digest = sys.argv[1:]
    assert os.geteuid() == 0 and platform.machine() in ("x86_64", "amd64")
    for path in (root, helper):
        assert path.startswith("/") and path != "/"
        assert str(pathlib.PurePosixPath(path)) == path
        assert not any(part in (".", "..") for part in path.split("/")[1:])
    assert root != helper and not helper.startswith(root + "/") and not root.startswith(helper + "/")
    user = pwd.getpwnam(username)
    assert user.pw_uid != 0
    payload = sys.stdin.buffer.read((32 << 20) + 1)
    assert 0 < len(payload) <= 32 << 20
    assert hashlib.sha256(payload).hexdigest() == digest.lower()
    flags = os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK
    with contextlib.ExitStack() as stack:
        def keep(fd):
            stack.callback(os.close, fd)
            return fd

        def parent(path):
            fd = keep(os.open("/", flags | os.O_DIRECTORY))
            for part in pathlib.PurePosixPath(path).parent.parts[1:]:
                created = False
                try:
                    os.mkdir(part, 0o755, dir_fd=fd)
                    created = True
                except FileExistsError:
                    pass
                fd = keep(os.open(part, flags | os.O_DIRECTORY, dir_fd=fd))
                info = os.fstat(fd)
                assert info.st_uid == 0 and info.st_mode & 0o022 == 0
                if created:
                    # sudo/image umask must not prevent the guest user from
                    # traversing newly created, root-owned helper/root parents.
                    os.fchmod(fd, 0o755)
            return fd

        helper_parent = parent(helper)
        lock = keep(os.open(".labbit-workspace-bootstrap.lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600, dir_fd=helper_parent))
        info = os.fstat(lock)
        assert stat.S_ISREG(info.st_mode) and info.st_uid == 0 and info.st_mode & 0o077 == 0
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        root_parent = parent(root)
        root_name = pathlib.PurePosixPath(root).name
        try:
            os.mkdir(root_name, 0o750, dir_fd=root_parent)
        except FileExistsError:
            pass
        else:
            # The protected parent excludes a guest rename before this open.
            root_fd = keep(os.open(root_name, flags | os.O_DIRECTORY, dir_fd=root_parent))
            os.fchown(root_fd, user.pw_uid, user.pw_gid)
            os.fchmod(root_fd, 0o750)
        root_fd = keep(os.open(root_name, flags | os.O_DIRECTORY, dir_fd=root_parent))
        info = os.fstat(root_fd)
        assert info.st_uid == user.pw_uid and info.st_gid == user.pw_gid
        assert stat.S_IMODE(info.st_mode) == 0o750
        helper_name = pathlib.PurePosixPath(helper).name
        try:
            fd = os.open(helper_name, flags, dir_fd=helper_parent)
        except FileNotFoundError:
            temp = ".labbit-helper-" + secrets.token_hex(16)
            try:
                fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o755, dir_fd=helper_parent)
                with os.fdopen(fd, "wb") as output:
                    output.write(payload)
                    output.flush()
                    os.fchmod(output.fileno(), 0o755)
                    os.fsync(output.fileno())
                # Never replace an existing object, including a symlink.
                os.link(temp, helper_name, src_dir_fd=helper_parent, dst_dir_fd=helper_parent, follow_symlinks=False)
                os.fsync(helper_parent)
            finally:
                try:
                    os.unlink(temp, dir_fd=helper_parent)
                except FileNotFoundError:
                    pass
            fd = os.open(helper_name, flags, dir_fd=helper_parent)
        with os.fdopen(fd, "rb") as installed:
            info = os.fstat(installed.fileno())
            assert stat.S_ISREG(info.st_mode) and info.st_uid == 0
            assert info.st_mode & 0o022 == 0 and info.st_mode & 0o111 != 0
            assert info.st_size == len(payload)
            assert hashlib.sha256(installed.read((32 << 20) + 1)).hexdigest() == digest.lower()
        probe_helper(helper, root, username)


def probe_helper(helper, root, username):
    # Exchange packets interactively: closing stdin before worker replies can
    # legitimately end SFTP before the STAT response is flushed.
    process = subprocess.Popen(["runuser", "-u", username, "--", helper, "--root", root], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, start_new_session=True)
    try:
        deadline = time.monotonic() + 10
        with selectors.DefaultSelector() as selector:
            selector.register(process.stdout, selectors.EVENT_READ)
            consumed = 0
            def read_exact(size):
                nonlocal consumed
                assert size >= 0 and consumed + size <= 65536
                data = bytearray()
                while len(data) < size:
                    remaining = deadline - time.monotonic()
                    assert remaining > 0 and selector.select(remaining)
                    chunk = os.read(process.stdout.fileno(), size - len(data))
                    assert chunk
                    data.extend(chunk)
                consumed += size
                return bytes(data)

            def exchange(payload):
                process.stdin.write(struct.pack(">I", len(payload)) + payload)
                process.stdin.flush()
                size = struct.unpack(">I", read_exact(4))[0]
                assert 0 < size <= 65532
                return read_exact(size)

            version = exchange(b"\x01" + struct.pack(">I", 3))
            assert version[:5] == b"\x02\x00\x00\x00\x03"
            attributes = exchange(b"\x11" + struct.pack(">II", 1, 1) + b"/")
            assert len(attributes) >= 9 and attributes[:5] == b"\x69\x00\x00\x00\x01"
        process.stdin.close()
        assert process.wait(timeout=1) in (0, 1)
    finally:
        # Include descendants even when runuser exits before its helper does.
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait(timeout=3)
        process.stdout.close()
        if not process.stdin.closed:
            process.stdin.close()


if __name__ == "__main__":
    try:
        main()
    except Exception:
        # Only the exit status crosses the SSH boundary.
        sys.exit(1)
