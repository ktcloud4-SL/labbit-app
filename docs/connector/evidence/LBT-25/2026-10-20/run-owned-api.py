import pathlib,subprocess,sys,os
from run_output import reserve_output
output = reserve_output('api-initial.json')
keyroot=pathlib.Path.home()/'.config/labbit/c3-connector-20261019'
script='PASSWORD='+repr(os.environ['C3_PASSWORD'])+'\n'+(pathlib.Path(__file__).parent/'api-probe.py').read_text(encoding='utf-8')
args=['ssh','-o','BatchMode=yes','-o','IdentitiesOnly=yes','-o','ConnectTimeout=8','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile='+str(keyroot/'lbt145-bootstrap-known_hosts'),'-o','HostKeyAlias=a275506e-c0cd-4f36-a741-420f06afaca5.lbt145-bootstrap','-i',str(keyroot/'bootstrap-openssh.pem'),'labbit@211.184.227.239','python3 -']
r=subprocess.run(args,input=script.encode(),capture_output=True,timeout=80)
output.write_bytes(r.stdout)
sys.stdout.buffer.write(r.stdout)
if r.returncode:
    categories=[s for s in ['Connection timed out','Connection refused','Permission denied','Host key verification failed','No route to host','Network is unreachable','Connection closed','Connection reset'] if s.lower() in r.stderr.decode(errors='replace').lower()]
    print('probeExit',r.returncode,'stderrBytes',len(r.stderr),'categories',categories)
raise SystemExit(r.returncode)
