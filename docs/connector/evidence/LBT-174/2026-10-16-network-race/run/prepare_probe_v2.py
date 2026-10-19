from pathlib import Path
import json,subprocess as sp
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
V2=OUT/'probe-v2';V2.mkdir(exist_ok=True)
text=(OUT/'local_probe_test.go').read_text(encoding='utf8')
text=text.replace('if err := phase("linux-preflight",','if os.Getenv("LABBIT_LOCAL_PROBE_ONLY")=="1" { return }\nif err := phase("linux-preflight",',1)
old=""" s=socket.socket(socket.AF_INET,socket.SOCK_STREAM);s.settimeout(5);start=time.monotonic();code=s.connect_ex(('1.1.1.1',443));s.close()
 return {'routeBefore':before,'defaultRoutes':[{'role':role(x.get('dev','')),'metric':x.get('metric',0),'hasGateway':'gateway' in x} for x in defaults],'connectCode':code,'elapsed_ms':round((time.monotonic()-start)*1000)}"""
new=""" s=socket.socket(socket.AF_INET,socket.SOCK_STREAM);s.settimeout(5);start=time.monotonic();code=s.connect_ex(('1.1.1.1',443));elapsed=round((time.monotonic()-start)*1000);source,port=s.getsockname();s.close()
 addresses=json.loads(subprocess.check_output(['ip','-j','address','show']))
 socketrole=next((role(x['ifname']) for x in addresses if any(y.get('local')==source for y in x.get('addr_info',[]))),'unknown')
 try:
  exact=json.loads(subprocess.check_output(['ip','-j','route','get','1.1.1.1','from',source,'ipproto','tcp','sport',str(port),'dport','443'],stderr=subprocess.DEVNULL))
  exactroles=[{'role':role(x.get('dev','')),'hasGateway':'gateway' in x} for x in exact]
 except subprocess.CalledProcessError: exactroles=[{'queryFailed':True}]
 rpf={role(x.get('dev','')):int((Path('/proc/sys/net/ipv4/conf')/x['dev']/'rp_filter').read_text().strip()) for x in defaults}
 return {'routeBefore':before,'routeAfter':route(),'socketSourceRole':socketrole,'socketTupleRoute':exactroles,'reversePathFilter':rpf,'defaultRoutes':[{'role':role(x.get('dev','')),'metric':x.get('metric',0),'hasGateway':'gateway' in x} for x in defaults],'connectCode':code,'elapsed_ms':elapsed}"""
assert old in text;text=text.replace(old,new)
(V2/'local_probe_test.go').write_text(text,encoding='utf8')
sp.run([r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe','-w',str(V2/'local_probe_test.go')],check=True)
overlay={'Replace':{str(ROOT/'internal/connector/provider/openstack/startup.go'):str(OUT/'startup_observed.go'),str(ROOT/'internal/connector/provider/openstack/local_network_race_probe_test.go'):str(V2/'local_probe_test.go')}}
(V2/'overlay.json').write_text(json.dumps(overlay,indent=2)+'\n',encoding='utf8')
runner=(OUT/'run_validation.py').read_text(encoding='utf8')
runner=runner.replace("ROOT=OUT.parent/'labbit-connector-gates-20261015'","ROOT=OUT.parent.parent/'labbit-connector-gates-20261015'")
runner=runner.replace("OUT/'source-manifest.json'","OUT.parent/'source-manifest.json'")
runner=runner.replace("before=identity();assert", "env['LABBIT_LOCAL_PROBE_ONLY']='1'\nbefore=identity();assert")
runner=runner.replace("['overlay.json','startup_observed.go','local_probe_test.go','linux_ci.sh']","['overlay.json','local_probe_test.go']")
start=runner.index(" with (OUT/'postgres-control.log').open('ab') as log:")
end=runner.index(" with (OUT/'local-network-race.log').open('wb') as log:",start)
runner=runner[:start]+runner[end:]
start=runner.index(" with (OUT/'postgres-cleanup.log').open('wb') as log:")
end=runner.index(" result.update(",start)
runner=runner[:start]+runner[end:]
(V2/'run_validation.py').write_text(runner,encoding='utf8')
print('v2 fresh-VM exact socket-source diagnostic prepared; production source unchanged',flush=True)
