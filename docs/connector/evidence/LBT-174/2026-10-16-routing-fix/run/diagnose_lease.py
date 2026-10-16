from pathlib import Path
import shutil,subprocess as sp,json
OUT=Path(__file__).resolve().parent
save=OUT/'failed-empty-policy-v2';save.mkdir(exist_ok=True)
for p in OUT.iterdir():
 if p.is_file() and (p.name.startswith(('probe-','internet-first-','renew-','route-counterfactual','diagnostic-timeout','after-timeout')) or p.name=='local_probe_test.go'):shutil.copy2(p,save/p.name)
p=OUT/'local_probe_test.go';s=p.read_text(encoding='utf8')
start=s.index('\t// No target connection');end=s.index('\n\tif os.Getenv("LABBIT_LOCAL_PROBE_ONLY")',start)
replacement='''
    dhcpPorts,err:=ports.List(a.network,ports.ListOpts{NetworkID:management.ID,DeviceOwner:"network:dhcp"}).AllPages(ctx)
    if err!=nil{t.Fatal("DHCP port lookup failed")}
    ownedDHCP,err:=ports.ExtractPorts(dhcpPorts);if err!=nil{t.Fatal("DHCP ports decode failed")}
    expected:=[]string{};for _,port:=range ownedDHCP{for _,ip:=range port.FixedIPs{expected=append(expected,ip.IPAddress)}}
    expectedJSON,_:=json.Marshal(expected)
    script:=localNetworkPrelude(labPort.MACAddress,p.MACAddress)+"expected=json.loads("+fmt.Sprintf("%q",string(expectedJSON))+")\\n"+`links=json.loads(bounded_output(['ip','-j','link']))
for link in links:
 if role(link['ifname'])=='unknown':continue
 lease=Path('/run/systemd/netif/leases')/str(link['ifindex'])
 values={}
 for line in lease.read_text().splitlines():
  key,sep,value=line.partition('=')
  if key in ('SERVER_ADDRESS','ROUTER','ROUTES','ADDRESS','NETMASK'):values[key]=value
 print(json.dumps({'role':role(link['ifname']),'lease':values,'serverIsNeutronDHCPPort':values.get('SERVER_ADDRESS') in expected}),flush=True)
print(json.dumps({'probe':probe()}),flush=True)
`
    if err:=phase("dhcp-lease-origin",localPython(script),30*time.Second);err!=nil{t.Fatal("lease origin diagnostic failed")}
'''
s=s[:start]+replacement+s[end:]
s=s.replace('"context"','"context"\n"encoding/json"')
p.write_text(s,encoding='utf8')
sp.run([r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe','-w',str(p)],check=True)
print('lease diagnostic only: no guest route mutations')
