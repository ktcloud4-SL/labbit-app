from pathlib import Path
import json,shutil,subprocess as sp,hashlib
BASE=Path(__file__).resolve().parent
SRC=BASE/'final-verified'
OUT=BASE/'off-lifecycle'
OUT.mkdir(exist_ok=True)
text=(SRC/'local_probe_test.go').read_text(encoding='utf8')
text=text.replace('InternetOutbound: true','InternetOutbound: false').replace('"lbt174-race-"','"lbt174-off-"')
start=text.index('\tmgmtSubnet, err := subnets.Get')
end=text.index('\tfor _, order := range []string{"lab-first", "management-first"}',start)
text=text[:start]+text[end:]
text=text.replace('\t"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"\n','')
start=text.index('\tif os.Getenv("LABBIT_LOCAL_PROBE_ONLY") == "1" {')
end=text.index('\tmarker := ',start)
text=text[:start]+'\tclient.Close()\n'+text[end:]
for imp in ['\t"github.com/pkg/sftp"\n','\t"io"\n','\t"strings"\n']:
 text=text.replace(imp,'')
text=text.replace(" rows=json.loads(bounded_output(['ip','-j','route','get','1.1.1.1']))", " try: rows=json.loads(bounded_output(['ip','-j','route','get','1.1.1.1'],stderr=subprocess.DEVNULL))\n except subprocess.CalledProcessError: return []")
text=text.replace("assert result['connectCode']==0 and len(result['defaultRoutes'])==1 and result['defaultRoutes'][0]['role']=='lab' and result['socketSourceRole']=='lab', 'unexpected Internet routing'", "assert result['connectCode']!=0 and len(result['defaultRoutes'])==0 and result['routeBefore']==[] and result['routeAfter']==[], 'Internet OFF route or reachability leak'")
text=text.replace("assert len(result['defaultRoutes'])==1 and result['defaultRoutes'][0]['role']=='lab'\nassert result['connectCode']==0 and result['socketSourceRole']=='lab'", "assert len(result['defaultRoutes'])==0 and result['routeBefore']==[] and result['routeAfter']==[]\nassert result['connectCode']!=0, 'Internet OFF reachability leak'")
text=text.replace('first socket failed after routing fix','Internet OFF strict routing failed').replace('Linux race fixture exact tracked absence checked','Internet OFF fixture exact tracked absence checked')
assert 'InternetOutbound: false' in text and 'InternetOutbound: true' not in text
assert 'sftp.NewClient' not in text and 'subnets.Get' not in text and 'LABBIT_LOCAL_PROBE_ONLY' not in text
(OUT/'local_probe_test.go').write_text(text,encoding='utf8')
gofmt=r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe'
sp.run([gofmt,'-w',str(OUT/'local_probe_test.go')],check=True)
overlay=json.loads((SRC/'overlay.json').read_text())
overlay['Replace']={k:str(OUT/'local_probe_test.go') for k in overlay['Replace']}
(OUT/'overlay.json').write_text(json.dumps(overlay,indent=2)+'\n',encoding='utf8')
for name in ['source-manifest.json','linux-source-archive.json','linux_ci.sh','run_validation.py']:
 shutil.copyfile(SRC/name,OUT/name)
meta={'purpose':'external validation only: OFF strict zero defaults, failed outbound TCP, authenticated pinned SSH, both DHCP renewal orders before/after actual reboot and immutable Reset g2','sourceSource':'final-verified','files':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in OUT.iterdir() if p.is_file()}}
(OUT/'preparation.json').write_text(json.dumps(meta,indent=2)+'\n',encoding='utf8')
print(json.dumps({'overlayReady':True,'path':str(OUT),'helperBytes':len(text)}))
