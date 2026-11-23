"""Cloud-free verification; creates only an owned loopback PostgreSQL cluster.

Usage: python this.py ROOT EVIDENCE GO_EXE NODE_EXE NPM_CLI PG_BIN SCRATCH
No inherited LABBIT/OS_/AWS/PG environment is passed to tests.
"""
import concurrent.futures, hashlib, json, os, pathlib, socket, subprocess, sys, time
from datetime import datetime, timezone

root, evidence, go, node, npm, pg, scratch = map(pathlib.Path, sys.argv[1:])
evidence.mkdir(parents=True, exist_ok=True)
scratch.mkdir(parents=True, exist_ok=True)
env = {k: v for k, v in os.environ.items() if not k.upper().startswith(("AWS", "LABBIT", "OS_", "PG"))}
env.update(PYTHONUTF8="1", NO_COLOR="1", CI="1")
env["PATH"] = str(go.parent) + os.pathsep + env.get("PATH", "")
records = []

def stamp(): return datetime.now(timezone.utc).isoformat()
def run(name, args, cwd=root, extra=None, timeout=900):
    started = stamp()
    with (evidence / name).open("wb") as f:
        try:
            p = subprocess.run(list(map(str,args)), cwd=cwd, env=env | (extra or {}), stdout=f, stderr=subprocess.STDOUT, timeout=timeout)
            code = p.returncode
        except subprocess.TimeoutExpired:
            code = 124
    record = dict(log=name, argv=list(map(str,args)), started_at_utc=started, ended_at_utc=stamp(), exit_code=code)
    records.append(record)
    (evidence / "commands.json").write_text(json.dumps(records, ensure_ascii=False, indent=2)+"\n", encoding="utf-8")
    print(name, code, flush=True)
    return code

old = json.loads((root/"evidence/LBT-60/2026-11-21-regression-diagnosis/source-manifest.json").read_text(encoding="utf-8-sig"))
files=[]
for item in old["files"]:
    b=(root/item["path"]).read_bytes()
    files.append(dict(path=item["path"],size=len(b),sha256=hashlib.sha256(b).hexdigest()))
(evidence/"source-manifest.json").write_text(json.dumps(dict(created_at_utc=stamp(),files=files, previous_manifest="../2026-11-21-regression-diagnosis/source-manifest.json", unchanged_from_prior=files==old["files"]),indent=2)+"\n",encoding="utf-8")

def basic_go():
    run("go-all.jsonl", [go,"test","-json","-count=1","-timeout=10m","./..."])
    run("go-vet.log", [go,"vet","./..."])
    run("go-build.log", [go,"build","./..."])
    run("go-control-race.jsonl", [go,"test","-race","-json","-count=1","./internal/drcontrol","./cmd/labbit-dr-control","./internal/server/maintenance","./internal/server/workercontrol","./internal/server/provision","./internal/server/app","./internal/server/connector","./internal/server/connectorwss"])

def web():
    for task in ("typecheck","lint","test","build"):
        run("web-"+task+".log", [node,npm,"run",task], cwd=root/"web")

with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
    jobs=[executor.submit(basic_go), executor.submit(web)]
    for job in jobs: job.result()

port=55439
s=socket.socket()
if s.connect_ex(("127.0.0.1",port))==0:
    raise SystemExit("Refusing to use an existing listener")
s.close()
data=scratch/"pgdata"
if data.exists(): raise SystemExit("Refusing to reuse an existing cluster")
if run("pg-init.log",[pg/"initdb.exe","-D",data,"-U","postgres","-A","trust","--encoding=UTF8","--locale=C"]) != 0:
    raise SystemExit("initdb failed")
started=False
try:
    if run("pg-start.log",[pg/"pg_ctl.exe","-D",data,"-l",scratch/"pg-server.log","-o",f"-p {port} -h 127.0.0.1 -c max_connections=200","-w","start"]) != 0:
        raise SystemExit("pg start failed")
    started=True
    dsn=f"postgres://postgres@127.0.0.1:{port}/postgres?sslmode=disable"
    extra={"LABBIT_TEST_DATABASE_DSN":dsn}
    pkgs=["./internal/postgres","./internal/postgres/postgrestest","./internal/server/app","./internal/server/auth","./internal/server/httpapi","./internal/drcontrol"]
    run("integration-vet.log",[go,"vet","-tags","integration",*pkgs],extra=extra)
    run("integration-all.jsonl",[go,"test","-race","-json","-tags","integration","-count=1","-timeout=10m",*pkgs],extra=extra)
    run("pg-databases.log",[pg/"psql.exe","-h","127.0.0.1","-p",str(port),"-U","postgres","-d","postgres","-Atc","SELECT datname FROM pg_database WHERE datname LIKE 'labbit_it_%'"])
finally:
    if started: run("pg-stop.log",[pg/"pg_ctl.exe","-D",data,"-m","fast","-w","stop"])
    s=socket.socket(); listening=s.connect_ex(("127.0.0.1",port))==0; s.close()
    (evidence/"cleanup.json").write_text(json.dumps(dict(port=port,listening_after_cleanup=listening,data_retained=True,owned_fixture_only=True),indent=2)+"\n",encoding="utf-8")

formatted=[]
for f in sorted(root.rglob("*.go")):
    if "vendor" in f.relative_to(root).parts: continue
    b=f.read_bytes().replace(b"\r\n",b"\n")
    p=subprocess.run([str(go.parent/"gofmt.exe")],input=b,capture_output=True)
    if p.returncode or p.stdout != b: formatted.append(f.relative_to(root).as_posix())
(evidence/"format.json").write_text(json.dumps(dict(mode="canonical LF bytes; checkout untouched",noncanonical=formatted),indent=2)+"\n",encoding="utf-8")
print("verification finished",flush=True)
