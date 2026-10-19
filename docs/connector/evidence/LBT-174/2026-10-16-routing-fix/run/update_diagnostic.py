from pathlib import Path
import json,subprocess as sp
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
p=OUT/'local_probe_test.go'
s=p.read_text(encoding='utf8')
start=s.index('\tphase := func(');end=s.index('\n\tif err := phase("route-counterfactual"',start)
s=s[:start]+'''\tphase := func(name, command string, limit time.Duration) error {
        phaseContext,cancelPhase:=context.WithTimeout(ctx,limit)
        defer cancelPhase()
        phaseClient:=localPinnedSSH(t,phaseContext,a,address,host)
        log,err:=os.Create(filepath.Join(out,name+".log"));if err!=nil {phaseClient.Close();return err}
        defer log.Close()
        begin:=time.Now()
        err=integrationDiagnosticSSH(phaseContext,phaseClient,command,log)
        status:=0;var exit *ssh.ExitError
        if errors.As(err,&exit){status=exit.ExitStatus()}else if err!=nil{status=-1}
        t.Logf("Linux validation phase=%s exit=%d elapsed_ms=%d joined=true",name,status,time.Since(begin).Milliseconds())
        return err
    }'''+s[end:]
s=s.replace('t.Logf("Internet first-series result attempt=%d success=%t elapsed_ms=%d", attempt, err == nil, time.Since(begin).Milliseconds())','t.Logf("Internet first-series result attempt=%d success=%t elapsed_ms=%d", attempt, err == nil, time.Since(begin).Milliseconds())\n if err!=nil{t.Error("first socket failed after routing fix")}')
s=s.replace('"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"','"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"\n"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"')
needle='\tif err := phase("route-counterfactual"'
pos=s.index(needle)
s=s[:pos]+'''    mgmtSubnet,err:=subnets.Get(ctx,a.network,p.FixedIPs[0].SubnetID).Extract()
    if err!=nil || net.ParseIP(mgmtSubnet.GatewayIP)==nil {t.Fatal("management gateway unavailable for owned diagnostic")}
    if err:=phase("diagnostic-timeout-cleanup",localTimeoutCleanupCommand(labPort.MACAddress,p.MACAddress),15*time.Second);err!=nil{t.Fatal("remote bounded timeout cleanup failed")}
    if err:=phase("after-timeout",localReachabilityCommand(labPort.MACAddress,p.MACAddress),15*time.Second);err!=nil{t.Fatal("fresh diagnostic after timeout failed")}
'''+s[pos:]
s=s.replace('localRouteABCommand(labPort.MACAddress, p.MACAddress), 45*time.Second','localRouteABCommand(labPort.MACAddress, p.MACAddress,mgmtSubnet.GatewayIP), 45*time.Second')
s=s.replace('func localRouteABCommand(labMAC, managementMAC string) string','func localRouteABCommand(labMAC, managementMAC, managementGateway string) string')
s=s.replace("localNetworkPrelude(labMAC, managementMAC) + `defaults=", "localNetworkPrelude(labMAC, managementMAC) + fmt.Sprintf(\"managementGateway=%q\\n\",managementGateway) + `defaults=",1)
s=s.replace(" selected=next(x for x in defaults if role(x.get('dev',''))==wanted and 'gateway' in x)"," selected=next(x for x in defaults if role(x.get('dev',''))==wanted and 'gateway' in x) if wanted=='lab' else {'gateway':managementGateway,'dev':next(x['ifname'] for x in json.loads(subprocess.check_output(['ip','-j','link'])) if role(x['ifname'])=='management')}\n print(json.dumps({'step':'selected','case':wanted}),flush=True)")
s=s.replace(" subprocess.run(['sudo','ip','route','add']+args,check=True,stdout=subprocess.DEVNULL)"," subprocess.run(['sudo','-n','ip','route','add']+args,check=True,stdout=subprocess.DEVNULL)\n print(json.dumps({'step':'route-added'}),flush=True)")
s=s.replace("finally: subprocess.run(['sudo','ip','route','del']+args,check=True,stdout=subprocess.DEVNULL)","finally:\n  subprocess.run(['sudo','-n','ip','route','del']+args,check=True,stdout=subprocess.DEVNULL)\n  print(json.dumps({'step':'route-removed','case':wanted}),flush=True)")
# Bound every diagnostic ip/sudo child. apt commands have their own GNU timeout.
s=s.replace('subprocess.check_output(', 'bounded_output(').replace('subprocess.run(', 'bounded_run(')
s=s.replace('from pathlib import Path\nmacs=', '''from pathlib import Path
import signal
def bounded_output(args,**kwargs):
 return subprocess.check_output(args,timeout=8,**kwargs)
def bounded_run(args,**kwargs):
 return subprocess.run(args,timeout=310 if 'apt-get' in args else 8,**kwargs)
def diagnostic_deadline(signum,frame): raise TimeoutError('bounded diagnostic deadline')
signal.signal(signal.SIGALRM,diagnostic_deadline)
signal.alarm(30)
macs=''')
# Long apt phase manages its own child timeouts; do not use the route probe alarm.
s=s.replace(" + `defaults=json.loads(bounded_output(['ip','-j','route','show','default']))\nselected=", " + `signal.alarm(0)\ndefaults=json.loads(bounded_output(['ip','-j','route','show','default']))\nselected=")
# Only role output is recorded. The owned route is exact, absent initially, and
# cleanup is verified even when the intentionally blocked diagnostic times out.
s+='''
func localTimeoutCleanupCommand(labMAC, managementMAC string) string {
return localPython(localNetworkPrelude(labMAC,managementMAC)+`target='1.0.0.1/32'
assert not json.loads(bounded_output(['ip','-j','route','show','exact',target]))
selected=next(x for x in json.loads(bounded_output(['ip','-j','route','show','default'])) if role(x.get('dev',''))=='lab')
args=[target,'via',selected['gateway'],'dev',selected['dev'],'metric','51']
bounded_run(['sudo','-n','ip','route','add']+args,check=True,stdout=subprocess.DEVNULL)
timedout=False
try:
 signal.alarm(1)
 time.sleep(60)
except TimeoutError: timedout=True
finally:
 signal.alarm(0)
 bounded_run(['sudo','-n','ip','route','del']+args,check=True,stdout=subprocess.DEVNULL)
assert timedout
assert not json.loads(bounded_output(['ip','-j','route','show','exact',target]))
print(json.dumps({'intentionalTimeoutObserved':True,'ownedRouteAbsent':True,'cleanupComplete':True}),flush=True)
`)
}
'''
p.write_text(s,encoding='utf8')
sp.run([r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe','-w',str(p)],check=True)
overlay={'Replace':{str(ROOT/'internal/connector/provider/openstack/local_network_race_probe_test.go'):str(p)}}
(OUT/'overlay.json').write_text(json.dumps(overlay,indent=2)+'\n',encoding='utf8')
print('diagnostic overlay updated: dedicated phase connection, synchronous join, bounded subprocesses, intentional timeout cleanup')
