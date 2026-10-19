from pathlib import Path
import json, shutil, subprocess as sp
OUT=Path(__file__).resolve().parent
DEST=OUT/'lease-observer';DEST.mkdir(exist_ok=False)
ROOT=OUT.parent/'labbit-connector-gates-20261015'
for name in ['run_validation.py','source-manifest.json','linux-source-archive.json','linux_ci.sh']:
    shutil.copy2(OUT/name,DEST/name)
p=DEST/'run_validation.py';s=p.read_text(encoding='utf8').replace("ROOT=OUT.parent/'labbit-connector-gates-20261015'",'ROOT=Path('+repr(str(ROOT))+')');p.write_text(s,encoding='utf8')
s=(OUT/'local_probe_test.go').read_text(encoding='utf8')
start=s.index('\t// No target connection');end=s.index('\n\tif os.Getenv("LABBIT_LOCAL_PROBE_ONLY")',start)
replacement='''
    var observed managementPort
    if err:=ports.Get(ctx,a.network,managementID).ExtractInto(&observed);err!=nil{t.Fatal("owned policy lookup failed")}
    policyJSON,_:=json.Marshal(observed.DHCPOptions)
    t.Logf("owned Management DHCP options=%s",policyJSON)
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
for path in sorted(Path('/etc/netplan').glob('*.yaml')):
 # Only the known disposable fixture's network configuration, never cloud user data.
 print(json.dumps({'netplanFile':path.name,'content':bounded_output(['sudo','-n','cat',str(path)])}),flush=True)
print(json.dumps({'probe':probe()}),flush=True)
`
    if err:=phase("dhcp-lease-origin",localPython(script),30*time.Second);err!=nil{t.Fatal("lease origin diagnostic failed")}
'''
s=s[:start]+replacement+s[end:]
s=s.replace('"context"','"context"\n"encoding/json"')
(DEST/'local_probe_test.go').write_text(s,encoding='utf8')
(DEST/'overlay.json').write_text(json.dumps({'Replace':{str(ROOT/'internal/connector/provider/openstack/local_network_race_probe_test.go'):str(DEST/'local_probe_test.go')}},indent=2),encoding='utf8')
sp.run([r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe','-w',str(DEST/'local_probe_test.go')],check=True)
print('Read-only guest lease/policy/netplan observer prepared; owned fixture creation/cleanup only')
