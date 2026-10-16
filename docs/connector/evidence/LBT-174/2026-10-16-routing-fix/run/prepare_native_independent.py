from pathlib import Path
import json,os,subprocess as sp
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
DEST=OUT/'native-independent';DEST.mkdir(exist_ok=True)
for name in ['go1.27.1.linux-amd64.tar.gz','linux-source.tar.gz']:
 target=DEST/name
 if not target.exists():os.link(OUT/name,target)
for name in ['source-manifest.json','linux-source-archive.json','linux_ci.sh']:(DEST/name).write_bytes((OUT/name).read_bytes())
s=(OUT/'local_probe_test.go').read_text(encoding='utf8')
start=s.index('\t// No target connection');end=s.index('\n\tif os.Getenv("LABBIT_LOCAL_PROBE_ONLY")',start)
s=s[:start]+'''    t.Log("INDEPENDENT_NATIVE_ONLY: routing, renewal, reboot and Reset acceptance omitted; compiler installation uses a temporary owned Lab route")
'''+s[end:]
start=s.index('    // The native CI has returned') if '    // The native CI has returned' in s else s.index('\t// The native CI has returned')
end=s.index('\n}\n\nfunc localPython',start)
s=s[:start]+s[end:]
s=s.replace('\n\t"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"','').replace('\n\t"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"','')
start=s.index("bounded_run(['sudo','-n','timeout','300s','apt-get'",s.index('func localAptCommand'))
end=s.index("\nprint(json.dumps({'aptTemporaryRouteUsed':False})",start)
apt=s[start:end]
s=s[:start]+'''selected=next(x for x in json.loads(bounded_output(['ip','-j','route','show','default'])) if role(x.get('dev',''))=='lab' and 'gateway' in x)
args=['default','via',selected['gateway'],'dev',selected['dev'],'metric','40']
with owned_route(args):
'''+''.join(' '+line+'\n' for line in apt.splitlines())+s[end:]
s=s.replace("{'aptTemporaryRouteUsed':False}","{'aptTemporaryRouteUsed':True,'aptOwnedRouteAbsent':True}")
(DEST/'local_probe_test.go').write_text(s,encoding='utf8')
overlay={'Replace':{str(ROOT/'internal/connector/provider/openstack/local_network_race_probe_test.go'):str(DEST/'local_probe_test.go')}}
(DEST/'overlay.json').write_text(json.dumps(overlay,indent=2)+'\n',encoding='utf8')
runner=(OUT/'run_validation.py').read_text(encoding='utf8').replace("ROOT=OUT.parent/'labbit-connector-gates-20261015'",'ROOT=Path('+repr(str(ROOT))+')')
(DEST/'run_validation.py').write_text(runner,encoding='utf8')
sp.run([r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe','-w',str(DEST/'local_probe_test.go')],check=True)
print('isolated native-CI runner prepared; routing acceptance explicitly omitted')
