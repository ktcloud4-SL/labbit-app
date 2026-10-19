"""Independent raw native CI log gate; no execution or remote access."""
from collections import Counter
import hashlib
import json
from pathlib import Path

OUT = Path(__file__).resolve().parent
STAGE = OUT.parent / "lbt174-routing-postadmin-20261016/final-verified"
log = STAGE / "linux-ci.log"
raw = log.read_bytes()
text = raw.decode("utf-8-sig")
records = [json.loads(line) for line in text.splitlines() if line.startswith("{")]
source = next(r for r in records if "sourceFiles" in r)
events = [r for r in records if "Action" in r]
tests = Counter(e["Action"] for e in events if e.get("Test"))
packages = Counter(e["Action"] for e in events if not e.get("Test"))
parents = sorted(e["Test"] for e in events if e.get("Test") and "/" not in e["Test"] and e["Action"] == "pass")
# Names are recorded directly rather than silently equating C1 parents with OpenStack opt-ins.
assert len(parents) == 12
assert all(p.startswith("TestC1") for p in parents)
assert tests["fail"] == tests["skip"] == 0 and packages["fail"] == 0
assert packages["pass"] == 1
assert "WARNING: DATA RACE" not in text
assert source == {"sourceFiles": 521, "sourceSHA256": "7e8b2cac4e25d20bbb3d44b3a749cfcd0fe350689bf125fda18202bcbd722e31", "mismatches": 0}
markers = ["LBT174_LINUX_VET_PASS", "LBT174_LINUX_BUILD_PASS", "LBT174_LINUX_RACE_PASS"]
assert all(text.count(m) == 1 for m in markers)
assert text.startswith("go version go1.27.1 linux/amd64\n")
assert "\nlinux\namd64\n1\nlocal\n-mod=vendor -p=2\n" in text
report = {"source": source, "environment": {"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": 1, "GOTOOLCHAIN": "local", "Go": "1.27.1", "gcc": text.splitlines()[1]},
          "commands": {"vet": "go vet -tags 'backend_router_harness integration' ./internal/server/app ./internal/server/connector", "build": "go build -tags backend_router_harness -o /tmp/labbit-c1-server ./cmd/labbit-server", "race": "go test -race -tags 'backend_router_harness integration' -run '^TestC1' -count=1 -json ./internal/server/app"},
          "markers": markers, "testPassEvents": tests["pass"], "parentPass": len(parents),
          "parentNames": parents, "testSkip": tests["skip"], "testFail": tests["fail"],
          "packagePass": packages["pass"], "raceWarnings": 0,
          "rawSHA256": hashlib.sha256(raw).hexdigest(), "rawBytes": len(raw),
          "scope": "native tagged C1 with real PostgreSQL and Mock Provider; not the 12 actual OpenStack parents or entire Browser/SaaS C3",
          "outerLifecycle": "Subsequently verified separately in final-on-lifecycle-verification.json. Native markers alone do not prove reboot/Reset/cloud cleanup/child exit."}
(OUT / "native-ci-raw-verification.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
print(json.dumps({"parentPass": len(parents), "testPassEvents": tests["pass"], "skip": tests["skip"], "fail": tests["fail"], "raceWarnings": 0}))
