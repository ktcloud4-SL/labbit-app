import pathlib,subprocess,sys
from run_output import reserve_output
output = reserve_output('collector.json')
script=pathlib.Path(sys.argv[1]).read_bytes()
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=2','admin@172.16.8.10','python3 -'],input=script,capture_output=True,timeout=90)
output.write_bytes(r.stdout)
sys.stdout.buffer.write(r.stdout)
if r.returncode:print('collectorExit',r.returncode,'stderrBytes',len(r.stderr))
raise SystemExit(r.returncode)
