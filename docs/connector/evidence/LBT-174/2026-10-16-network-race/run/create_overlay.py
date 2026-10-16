from pathlib import Path
import json,subprocess as sp
OUT=Path(__file__).resolve().parent;ROOT=OUT.parent/'labbit-connector-gates-20261015'
source=(ROOT/'internal/connector/provider/openstack/startup.go').read_text(encoding='utf8')
source=source.replace('"context"','"context"\n"bytes"\n"encoding/json"\n"fmt"')
signature='func (a *Adapter) runSSHCommand(ctx context.Context, address, hostIdentity, command string) error {'
header='''func (a *Adapter) runSSHCommand(ctx context.Context, address, hostIdentity, command string) (result error) {
 started:=time.Now();phase:="key_read"
 var stdout,stderr bytes.Buffer
 var observedError error
 defer func(){
  commandKind:="other";if command=="true" {commandKind="ready"}else if command=="cloud-init status --wait"{commandKind="cloud-init"}else if strings.HasPrefix(command,"python3"){commandKind="internet-probe"}
  record:=map[string]any{"kind":commandKind,"stage":phase,"elapsed_ms":time.Since(started).Milliseconds(),"success":result==nil}
  if observedError!=nil {record["errorType"]=fmt.Sprintf("%T",observedError);var exit *ssh.ExitError;if errors.As(observedError,&exit){record["exitStatus"]=exit.ExitStatus()}}
  if commandKind=="internet-probe" {var probe map[string]any;if json.Unmarshal(stdout.Bytes(),&probe)==nil {record["probe"]=probe};record["reachabilityAssertion"]=strings.Contains(stderr.String(),"AssertionError: unexpected Internet reachability")}
  b,_:=json.Marshal(record);fmt.Printf("LBT174_SSH_OBSERVATION %s\\n",b)
 }()
'''
assert signature in source;source=source.replace(signature,header)
for before,label in [('signer, err := ssh.ParsePrivateKey(privateKey)','key_parse'),('hostKeyCallback, err := trustOnFirstUseCallback(config.SSHKnownHostsFile, hostIdentity)','host_pin_setup'),('dialer := net.Dialer{Timeout: 5 * time.Second}','tcp_dial'),('clientConnection, channels, requests, err := ssh.NewClientConn','ssh_handshake'),('session, err := client.NewSession()','ssh_channel')]:
 assert before in source;source=source.replace(before,'phase="'+label+'"\n'+before,1)
source=source.replace('if err := session.Run(command); err != nil {','session.Stdout=&stdout;session.Stderr=&stderr\nphase="remote_exec"\nif err := session.Run(command); err != nil {\nobservedError=err',1)
start=source.index(header);end=source.index('\nfunc sshHostKeyIdentity',start)
part=source[start:end].replace('if err != nil {\n\t\treturn ErrStartupNotReady','if err != nil {\n observedError=err\n\t\treturn ErrStartupNotReady')
source=source[:start]+part+source[end:]
(OUT/'startup_observed.go').write_text(source,encoding='utf8')
gofmt=r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe'
sp.run([gofmt,'-w',str(OUT/'startup_observed.go'),str(OUT/'local_probe_test.go')],check=True)
overlay={'Replace':{str(ROOT/'internal/connector/provider/openstack/startup.go'):str(OUT/'startup_observed.go'),str(ROOT/'internal/connector/provider/openstack/local_network_race_probe_test.go'):str(OUT/'local_probe_test.go')}}
(OUT/'overlay.json').write_text(json.dumps(overlay,indent=2)+'\n',encoding='utf8')
print('external observation overlay created; no repository code changed')
