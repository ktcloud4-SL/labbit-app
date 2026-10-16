from pathlib import Path
import subprocess as sp
OUT=Path(__file__).resolve().parent
p=OUT/'local_probe_test.go';s=p.read_text(encoding='utf8')
s=s.replace('"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"','"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"\n"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"')
anchor='\tt.Log("Linux native CGO/race CI command completed; JSONL and exact required tests checked separately")'
s=s.replace(anchor,anchor+'''
    // The native CI has returned; no forwarded DB worker is needed across reboot/Reset.
    listener.Close();fileclient.Close();client.Close()
    marker:=`python3 -c "from pathlib import Path; Path('/home/ubuntu/lbt174-validation-boot').write_text(Path('/proc/sys/kernel/random/boot_id').read_text())"`
    if err:=phase("before-reboot",marker,15*time.Second);err!=nil{t.Fatal("boot identity marker failed")}
    if err:=servers.Reboot(ctx,a.compute,serverID,servers.RebootOpts{Type:servers.SoftReboot}).ExtractErr();err!=nil{t.Fatal("owned VM reboot failed")}
    rebootCtx,cancelReboot:=context.WithTimeout(ctx,5*time.Minute)
    bootChanged:=false
    for rebootCtx.Err()==nil {
        checkCtx,cancelCheck:=context.WithTimeout(rebootCtx,10*time.Second)
        err:=a.runSSHCommand(checkCtx,address,host,`python3 -c "from pathlib import Path; assert Path('/home/ubuntu/lbt174-validation-boot').read_text()!=Path('/proc/sys/kernel/random/boot_id').read_text()"`)
        cancelCheck()
        if err==nil{bootChanged=true;break}
        select{case <-time.After(2*time.Second):case <-rebootCtx.Done():}
    }
    cancelReboot()
    if !bootChanged{t.Fatal("reboot identity and authenticated SSH recovery not observed")}
    if err:=phase("after-reboot",localReachabilityCommand(labPort.MACAddress,p.MACAddress),15*time.Second);err!=nil{t.Fatal("routing after actual reboot failed")}
    for _,order:=range []string{"lab-first","management-first"}{if err:=phase("after-reboot-renew-"+order,localRenewCommand(labPort.MACAddress,p.MACAddress,order),45*time.Second);err!=nil{t.Fatal("renew after reboot failed")}}
    reset,resetErr:=a.Reset(ctx,coreprovider.ResetRequest{Correlation:coreprovider.Correlation{OperationID:labID+"-reset",LabInstanceID:labID,Generation:2},CreationSnapshot:snapshot,ProviderResources:resourceRefs(result.ProviderResources,1)})
    // Retain both confirmed generations for cleanup on every partial failure.
    for _,r:=range reset.ProviderResources{result.ProviderResources=upsertResource(result.ProviderResources,r)}
    if resetErr!=nil||reset.Outcome!=coreprovider.OutcomeSucceeded{t.Fatal("reset generation2 failed")}
    fresh:=resourceResultsForGeneration(reset.ProviderResources,2)
    serverID=providerResourceID(t,fresh,coreprovider.ResourceTypeServer,"workspace")
    managementID=providerResourceID(t,fresh,coreprovider.ResourceTypePort,"workspace:management")
    p,err=ports.Get(ctx,a.network,managementID).Extract();if err!=nil{t.Fatal("reset management lookup failed")}
    ip,err=managementAddress(*p);if err!=nil{t.Fatal("reset management address missing")}
    address=net.JoinHostPort(ip,"22");host,err=sshHostKeyIdentity(a.provision.ProviderConnectionID,serverID);if err!=nil{t.Fatal("reset host identity missing")}
    labPortID=providerResourceID(t,fresh,coreprovider.ResourceTypePort,"workspace:lab")
    labPort,err=ports.Get(ctx,a.network,labPortID).Extract();if err!=nil{t.Fatal("reset lab lookup failed")}
    for attempt:=1;attempt<=12;attempt++{if err:=phase(fmt.Sprintf("after-reset-first-%02d",attempt),localReachabilityCommand(labPort.MACAddress,p.MACAddress),15*time.Second);err!=nil{t.Fatal("reset first-series routing failed")}}
    for _,order:=range []string{"lab-first","management-first"}{if err:=phase("after-reset-renew-"+order,localRenewCommand(labPort.MACAddress,p.MACAddress,order),45*time.Second);err!=nil{t.Fatal("renew after reset failed")}}
    t.Log("actual reboot boot-id change, authenticated SSH recovery, immutable Reset g2, first12 and both renewal orders verified")
''')
p.write_text(s,encoding='utf8')
sp.run([r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe','-w',str(p)],check=True)
print('actual reboot and immutable Reset routing lifecycle added after native CI')
