from pathlib import Path
import json,shutil,subprocess as sp
OUT=Path(__file__).resolve().parent;DEST=OUT/'candidate-onlink';DEST.mkdir(exist_ok=False)
ROOT=OUT.parent/'labbit-connector-gates-20261015'
for name in ['run_validation.py','source-manifest.json','linux-source-archive.json','linux_ci.sh']:
    shutil.copy2(OUT/'lease-observer'/name,DEST/name)
s=(OUT/'lease-observer/local_probe_test.go').read_text(encoding='utf8')
s=s.replace("'content':bounded_output(['sudo','-n','cat',str(path)])", "'content':bounded_output(['sudo','-n','cat',str(path)]).decode('utf8')")
start=s.index('\n\tif os.Getenv("LABBIT_LOCAL_PROBE_ONLY")')
candidate='''
    var subnetBody struct {Subnet struct {CIDR string `json:"cidr"`} `json:"subnet"`}
    if _,err:=a.network.Get(ctx,a.network.ServiceURL("subnets",p.FixedIPs[0].SubnetID),&subnetBody,nil);err!=nil{t.Fatal("owned subnet lookup failed")}
    if _,_,err:=net.ParseCIDR(subnetBody.Subnet.CIDR);err!=nil{t.Fatal("subnet CIDR invalid")}
    candidateValue:=subnetBody.Subnet.CIDR+",0.0.0.0"
    body:=map[string]any{"port":map[string]any{"extra_dhcp_opts":[]map[string]any{
        {"opt_name":"router","opt_value":"","ip_version":4},
        {"opt_name":"classless-static-route","opt_value":candidateValue,"ip_version":4},
    }}}
    if _,err:=a.network.Put(ctx,a.network.ServiceURL("ports",managementID),body,nil,nil);err!=nil{t.Fatal("owned candidate policy update failed")}
    t.Log("CANDIDATE_ONLY: canonical nonempty on-link121+blankrouter on owned disposable port; production source unchanged")
    // Observe both a fresh ACK and current route table; preserve strict acceptance.
    if err:=phase("candidate-renew-management-first",localRenewCommand(labPort.MACAddress,p.MACAddress,"management-first"),45*time.Second);err!=nil{t.Error("candidate renewal acceptance failed")}
    if err:=phase("candidate-dhcp-lease",localPython(script),30*time.Second);err!=nil{t.Error("candidate observation failed")}
'''
s=s[:start]+candidate+s[start:]
(DEST/'local_probe_test.go').write_text(s,encoding='utf8')
(DEST/'overlay.json').write_text(json.dumps({'Replace':{str(ROOT/'internal/connector/provider/openstack/local_network_race_probe_test.go'):str(DEST/'local_probe_test.go')}},indent=2),encoding='utf8')
sp.run([r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe','-w',str(DEST/'local_probe_test.go')],check=True)
print('Owned-port onlink121 candidate observer prepared; source unchanged; no shared configuration or route mutation')
