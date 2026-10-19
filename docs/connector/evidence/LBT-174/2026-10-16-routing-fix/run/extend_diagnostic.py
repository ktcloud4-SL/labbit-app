from pathlib import Path
import subprocess as sp
OUT=Path(__file__).resolve().parent;p=OUT/'local_probe_test.go';s=p.read_text(encoding='utf8')
begin=s.index('\t// No target connection');end=s.index('\n\tclient := localPinnedSSH',begin)
loop=s[begin:end]
s=s[:begin]+s[end:]
position=s.index('\n\tmgmtSubnet, err :=')
loop=loop.replace('a.runSSHCommand(ctx, address, host, localReachabilityCommand(labPort.MACAddress, p.MACAddress))','phase(fmt.Sprintf("internet-first-%02d",attempt), localReachabilityCommand(labPort.MACAddress, p.MACAddress),15*time.Second)')
s=s[:position]+'\n'+loop+s[position:]
position=s.index('\n\tif os.Getenv("LABBIT_LOCAL_PROBE_ONLY")')
s=s[:position]+'''
    for _,order:=range []string{"lab-first","management-first"}{
        if err:=phase("renew-"+order,localRenewCommand(labPort.MACAddress,p.MACAddress,order),45*time.Second);err!=nil{t.Fatal("DHCP renew policy failed")}
    }
'''+s[position:]
s+='''
func localRenewCommand(labMAC,managementMAC,order string)string{
return localPython(localNetworkPrelude(labMAC,managementMAC)+fmt.Sprintf("order=%q\\n",order)+`sequence=['lab','management'] if order=='lab-first' else ['management','lab']
links=json.loads(bounded_output(['ip','-j','link']))
for wanted in sequence:
 link=next(x for x in links if role(x['ifname'])==wanted)
 lease=Path('/run/systemd/netif/leases')/str(link['ifindex'])
 before=lease.stat().st_mtime_ns
 bounded_run(['sudo','-n','networkctl','renew',link['ifname']],check=True,stdout=subprocess.DEVNULL)
 deadline=time.monotonic()+10
 while time.monotonic()<deadline:
  if lease.exists() and lease.stat().st_mtime_ns!=before:break
  time.sleep(0.1)
 else:raise RuntimeError('DHCP renewal completion not observed')
 print(json.dumps({'renewedRole':wanted,'leaseChanged':True}),flush=True)
result=probe()
assert len(result['defaultRoutes'])==1 and result['defaultRoutes'][0]['role']=='lab'
assert result['connectCode']==0 and result['socketSourceRole']=='lab'
print(json.dumps({'case':'after-renew-'+order,**result}),flush=True)
`)
}
'''
p.write_text(s,encoding='utf8')
sp.run([r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe','-w',str(p)],check=True)
print('first socket observation and two DHCP renewal orders added')
