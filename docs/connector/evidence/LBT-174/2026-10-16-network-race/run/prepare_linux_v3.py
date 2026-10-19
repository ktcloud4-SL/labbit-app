from pathlib import Path
import shutil,os,json,subprocess as sp
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
V3=OUT/'linux-v3';V3.mkdir(exist_ok=True)
for name in ['go1.27.1.linux-amd64.tar.gz','linux-source.tar.gz','linux_ci.sh','source-manifest.json','startup_observed.go']:
 if not (V3/name).exists():os.link(OUT/name,V3/name)
text=(OUT/'probe-v2/local_probe_test.go').read_text(encoding='utf8')
text=text.replace("'DEBIAN_FRONTEND=noninteractive','timeout'","'DEBIAN_FRONTEND=noninteractive','NEEDRESTART_MODE=l','timeout'")
old="finally: subprocess.run(['sudo','ip','route','del']+args,check=True,stdout=subprocess.DEVNULL)\n`)\n}\n\nfunc localPinnedSSH"
new="""finally:
 existing=json.loads(subprocess.check_output(['ip','-j','route','show','default']))
 ours=any(x.get('gateway')==selected['gateway'] and x.get('dev')==selected['dev'] and x.get('metric')==40 for x in existing)
 if ours: subprocess.run(['sudo','ip','route','del']+args,check=True,stdout=subprocess.DEVNULL)
 assert not any(x.get('gateway')==selected['gateway'] and x.get('dev')==selected['dev'] and x.get('metric')==40 for x in json.loads(subprocess.check_output(['ip','-j','route','show','default'])))
print(json.dumps({'aptOwnedRouteAbsent':True}),flush=True)
swap=Path('/home/ubuntu/lbt174-validation.swap')
assert not swap.exists()
subprocess.run(['sudo','fallocate','-l','1G',str(swap)],check=True)
subprocess.run(['sudo','chmod','600',str(swap)],check=True)
subprocess.run(['sudo','mkswap',str(swap)],check=True,stdout=subprocess.DEVNULL)
subprocess.run(['sudo','swapon',str(swap)],check=True)
print(json.dumps({'ownedDisposableSwapBytes':swap.stat().st_size}),flush=True)
`)
}

func localPinnedSSH"""
assert old in text;text=text.replace(old,new)
(V3/'local_probe_test.go').write_text(text,encoding='utf8')
sp.run([r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe','-w',str(V3/'local_probe_test.go')],check=True)
overlay={'Replace':{str(ROOT/'internal/connector/provider/openstack/startup.go'):str(V3/'startup_observed.go'),str(ROOT/'internal/connector/provider/openstack/local_network_race_probe_test.go'):str(V3/'local_probe_test.go')}}
(V3/'overlay.json').write_text(json.dumps(overlay,indent=2)+'\n',encoding='utf8')
runner=(OUT/'run_validation.py').read_text(encoding='utf8').replace("ROOT=OUT.parent/'labbit-connector-gates-20261015'","ROOT=OUT.parent.parent/'labbit-connector-gates-20261015'")
(V3/'run_validation.py').write_text(runner,encoding='utf8')
print('v3 native Linux setup: list-only needrestart, exact route-absence check, disposable 1GiB swap',flush=True)
