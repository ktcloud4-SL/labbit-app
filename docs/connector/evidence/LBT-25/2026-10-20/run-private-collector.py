import pathlib,subprocess,sys,json,yaml
from run_output import reserve_output
output = reserve_output('workspace-metadata.json')
folder=pathlib.Path.home()/'.config/labbit/c3-connector-20261019'
auth=yaml.safe_load((pathlib.Path.home()/'.config/openstack/ktcloud-d1/clouds.yaml').read_text(encoding='utf-8-sig'))['clouds']['ktcloud-d1']['auth']
ledger=json.loads((folder/'ktcloud-bootstrap-ledger.json').read_text(encoding='utf-8-sig'))
script=pathlib.Path(sys.argv[1]).read_text(encoding='utf-8-sig')
source='PRIVATE='+repr({'auth':auth,'ledger':ledger})+'\nimport json,sys\ntry:\n exec('+repr(script)+')\nexcept Exception as e:\n print(json.dumps({"collectorError":type(e).__name__,"httpStatus":getattr(e,"code",None) if isinstance(getattr(e,"code",None),int) else None}));sys.exit(1)\n'
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=2','admin@172.16.8.10','python3 -'],input=source.encode(),capture_output=True,timeout=90)
output.write_bytes(r.stdout)
sys.stdout.buffer.write(r.stdout)
if r.returncode:print('collectorExit',r.returncode,'stderrBytes',len(r.stderr))
raise SystemExit(r.returncode)
