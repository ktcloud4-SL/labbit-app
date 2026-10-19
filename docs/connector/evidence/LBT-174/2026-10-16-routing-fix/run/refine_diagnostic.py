from pathlib import Path
import subprocess as sp
OUT=Path(__file__).resolve().parent
p=OUT/'local_probe_test.go'
# The v2 complete generator is preserved; lease-origin investigation uses a separate run.
s=(OUT/'failed-empty-policy-v2/local_probe_test.go').read_text(encoding='utf8')
s=s.replace('import signal\n','''import signal
from contextlib import contextmanager
def owned_route_entries(args):
 rows=json.loads(bounded_output(['ip','-j','route','show','exact',args[0]]))
 return [x for x in rows if x.get('gateway')==args[2] and x.get('dev')==args[4] and x.get('metric',0)==int(args[6])]
@contextmanager
def owned_route(args):
 assert not owned_route_entries(args), 'owned diagnostic route already exists'
 try:
  bounded_run(['sudo','-n','ip','route','add']+args,check=True,stdout=subprocess.DEVNULL)
  yield
 finally:
  if owned_route_entries(args): bounded_run(['sudo','-n','ip','route','del']+args,check=True,stdout=subprocess.DEVNULL)
  assert not owned_route_entries(args), 'owned diagnostic route remains'
''',1)
s=s.replace(" bounded_run(['sudo','-n','ip','route','add']+args,check=True,stdout=subprocess.DEVNULL)\n print(json.dumps({'step':'route-added'}),flush=True)\n try:"," with owned_route(args):")
s=s.replace(" finally:\n  bounded_run(['sudo','-n','ip','route','del']+args,check=True,stdout=subprocess.DEVNULL)\n  print(json.dumps({'step':'route-removed','case':wanted}),flush=True)"," print(json.dumps({'step':'route-removed','case':wanted}),flush=True)")
start=s.index("defaults=json.loads",s.index('func localAptCommand'))
end=s.index("swap=Path(",start)
s=s[:start]+'''bounded_run(['sudo','-n','timeout','300s','apt-get','update','-qq'],check=True)
bounded_run(['sudo','-n','env','DEBIAN_FRONTEND=noninteractive','NEEDRESTART_MODE=l','timeout','300s','apt-get','install','-y','--no-install-recommends','gcc','libc6-dev'],check=True)
print(json.dumps({'aptTemporaryRouteUsed':False}),flush=True)
'''+s[end:]
start=s.index("bounded_run(['sudo','-n','ip','route','add']+args",s.index('func localTimeoutCleanupCommand'))
end=s.index('\nassert timedout',start)
s=s[:start]+'''timedout=False
with owned_route(args):
 try:
  signal.alarm(1)
  time.sleep(60)
 except TimeoutError: timedout=True
 finally: signal.alarm(0)'''+s[end:]
# First-attempt success alone is insufficient: require exactly the intended default route.
s=s.replace("assert result['connectCode']==0, 'unexpected Internet reachability'", "assert result['connectCode']==0 and len(result['defaultRoutes'])==1 and result['defaultRoutes'][0]['role']=='lab' and result['socketSourceRole']=='lab', 'unexpected Internet routing'")
s=s.replace("result=probe()\nassert len(result['defaultRoutes'])", "result=probe()\nprint(json.dumps({'case':'observed-after-renew-'+order,**result}),flush=True)\nassert len(result['defaultRoutes'])")
p.write_text(s,encoding='utf8')
sp.run([r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe','-w',str(p)],check=True)
print('route add unknown-response cleanup covered; apt no longer mutates routes')
