"""Opt-in Linux root filesystem acceptance; no cloud or existing workspace changes.

LABBIT_BOOTSTRAP_TEST_HELPER=/path/to/linux-helper python3 -m unittest discover \
  -s internal/connector/provider/openstack -p workspace_bootstrap_guest_test.py
"""
import hashlib
import os
import pathlib
import pwd
import shutil
import subprocess
import tempfile
import time
import unittest


@unittest.skipUnless(os.name == "posix" and os.geteuid() == 0 and os.environ.get("LABBIT_BOOTSTRAP_TEST_HELPER"), "requires Linux root and opt-in helper artifact")
class GuestBootstrapTest(unittest.TestCase):
    def setUp(self):
        self.base = pathlib.Path(tempfile.mkdtemp(prefix="labbit-bootstrap-test-", dir="/opt"))
        self.base.chmod(0o755)
        self.addCleanup(lambda: shutil.rmtree(self.base))
        self.root = self.base / "workspaces" / "workspace"
        self.helper = self.base / "libexec" / "helper"
        self.payload = pathlib.Path(os.environ["LABBIT_BOOTSTRAP_TEST_HELPER"]).read_bytes()
        self.digest = hashlib.sha256(self.payload).hexdigest()
        self.user = pwd.getpwnam("nobody")

    def run_bootstrap(self, digest=None):
        return subprocess.run(["python3", str(pathlib.Path(__file__).with_name("workspace_bootstrap.py")), str(self.root), str(self.helper), self.user.pw_name, digest or self.digest], input=self.payload, capture_output=True, timeout=20)

    def test_fresh_then_repeated_preserves_workspace_contents(self):
        self.assertEqual(self.run_bootstrap().returncode, 0)
        item = self.root / "existing.txt"
        item.write_bytes(b"preserve existing work")
        self.assertEqual(self.run_bootstrap(self.digest.upper()).returncode, 0)
        self.assertEqual(item.read_bytes(), b"preserve existing work")
        self.assertEqual(self.root.stat().st_uid, self.user.pw_uid)
        self.assertEqual(self.root.stat().st_mode & 0o777, 0o750)
        self.assertEqual(self.helper.stat().st_uid, 0)
        self.assertEqual(self.helper.stat().st_mode & 0o777, 0o755)
        self.assertEqual(self.helper.read_bytes(), self.payload)

    def test_existing_different_helper_is_not_overwritten(self):
        self.helper.parent.mkdir(mode=0o755)
        self.helper.write_bytes(b"different installation")
        self.helper.chmod(0o755)
        self.assertNotEqual(self.run_bootstrap().returncode, 0)
        self.assertEqual(self.helper.read_bytes(), b"different installation")

    def test_symlink_parent_does_not_write_outside(self):
        outside = self.base / "outside"
        outside.mkdir()
        self.helper.parent.symlink_to(outside, target_is_directory=True)
        self.assertNotEqual(self.run_bootstrap().returncode, 0)
        self.assertEqual(list(outside.iterdir()), [])

    def test_symlink_workspace_is_rejected(self):
        self.root.parent.mkdir(mode=0o755)
        outside = self.base / "outside"
        outside.mkdir()
        self.root.symlink_to(outside, target_is_directory=True)
        self.assertNotEqual(self.run_bootstrap().returncode, 0)
        self.assertEqual(list(outside.iterdir()), [])

    def test_unsafe_parent_is_not_repaired(self):
        self.helper.parent.mkdir(mode=0o755)
        self.helper.parent.chmod(0o777)
        self.assertNotEqual(self.run_bootstrap().returncode, 0)
        self.assertEqual(self.helper.parent.stat().st_mode & 0o777, 0o777)

    def test_overlapping_paths_do_not_create_paths(self):
        self.root = self.helper / "workspace"
        self.assertNotEqual(self.run_bootstrap().returncode, 0)
        self.assertEqual(list(self.base.iterdir()), [])

    def test_probe_rejects_malformed_and_oversized_responses(self):
        for response in (b"", b"\x00\x00", b"\x00\x01\x00\x01", b"\x00\x00\x00\x05\x02\x00\x00\x00\x04"):
            self.payload = b"#!/usr/bin/python3\nimport os\nos.write(1," + repr(response).encode() + b")\n"
            self.digest = hashlib.sha256(self.payload).hexdigest()
            result = self.run_bootstrap()
            self.assertNotEqual(result.returncode, 0)
            self.helper.unlink()

    def test_probe_timeout_kills_descendants(self):
        self.payload = b"#!/usr/bin/python3\nimport subprocess,sys,time,pathlib\np=subprocess.Popen(['sleep','60'])\npathlib.Path(sys.argv[-1]+'/probe.pid').write_text(str(p.pid))\ntime.sleep(60)\n"
        self.digest = hashlib.sha256(self.payload).hexdigest()
        started = time.monotonic()
        self.assertNotEqual(self.run_bootstrap().returncode, 0)
        self.assertLess(time.monotonic() - started, 15)
        pid = int((self.root / "probe.pid").read_text())
        status = pathlib.Path(f"/proc/{pid}/stat")
        # A reparented zombie is already dead and cannot run or hold handles.
        self.assertTrue(not status.exists() or status.read_text().split()[2] == "Z")

    def test_digest_failure_does_not_create_paths(self):
        self.assertNotEqual(self.run_bootstrap("0" * 64).returncode, 0)
        self.assertEqual(list(self.base.iterdir()), [])

    def test_unsafe_existing_workspace_is_preserved(self):
        self.assertEqual(self.run_bootstrap().returncode, 0)
        self.root.chmod(0o777)
        self.assertNotEqual(self.run_bootstrap().returncode, 0)
        self.assertEqual(self.root.stat().st_mode & 0o777, 0o777)


if __name__ == "__main__":
    unittest.main()
