from pathlib import Path
import shutil,subprocess as sp
OUT=Path(__file__).resolve().parent
ROOT=OUT.parent/'labbit-connector-gates-20261015'
save=OUT/'failed-api-v1';save.mkdir(exist_ok=True)
for name in ['source-manifest.json','linux-source-archive.json','probe-live.log','probe-run-result.json','probe-run-state.json','local_probe_test.go']:
 shutil.copy2(OUT/name,save/name)
base=ROOT/'internal/connector/provider/openstack'
p=base/'management_network.go';s=p.read_text(encoding='utf8')
s=s.replace('{Name: "3", Value: &empty, Version: 4}', '{Name: "router", Value: &empty, Version: 4}').replace('{Name: "121", Value: &empty, Version: 4}', '{Name: "classless-static-route", Value: &empty, Version: 4}').replace('\n\t\t{Name: "249", Value: &empty, Version: 4},','')
s=s.replace('len(options) != 3','len(options) != 2')
start=s.index('\t\tname := option.Name');end=s.index('\n\t\tseen[name] = true',start)
s=s[:start]+'''\t\tname := option.Name
        if (name != "router" && name != "classless-static-route") || seen[name] || option.Value == nil || *option.Value != "" || option.Version != 4 {
            return false
        }'''+s[end:]
p.write_text(s,encoding='utf8')
p=base/'management_network_test.go';s=p.read_text(encoding='utf8').replace('{Name: "3", Value: &empty, Version: 4}, {Name: "121", Value: &empty, Version: 4}, {Name: "249", Value: &empty, Version: 4}','{Name: "router", Value: &empty, Version: 4}, {Name: "classless-static-route", Value: &empty, Version: 4}')
s=s.replace('"nonempty", "unknown"','"nonempty", "unknown", "numeric name", "unsupported 249"').replace('opts = opts[:2]','opts = opts[:1]').replace('opts[2].Name = "router"','opts[1].Name = "router"').replace('case "unknown":','case "numeric name":\n opts[0].Name = "3"\ncase "unsupported 249":\n opts = append(opts, managementDHCPOption{Name:"249",Value:&empty,Version:4})\ncase "unknown":')
p.write_text(s,encoding='utf8')
p=base/'management_subnet_affinity_test.go';s=p.read_text(encoding='utf8').replace('"opt_name":"3"','"opt_name":"router"').replace('"opt_name":"121"','"opt_name":"classless-static-route"').replace(',{"opt_name":"249","opt_value":"","ip_version":4}','');p.write_text(s,encoding='utf8')
for rel in ['runtime/contract.yaml','contracts/connector/README.md']:
 p=ROOT/rel;s=p.read_text(encoding='utf8').replace('3/121/249','router(3)/classless-static-route(121)')
 if rel.startswith('runtime'):
  s=s.replace('지원 배포·이미지는 이 빈값을 실제 적용하는 DHCP agent와 DHCP 기반 guest이며','249 옵션의 빈값은 Neutron이 지원하지 않으므로 요청하지 않습니다.\n        지원 배포·이미지는 위 빈값을 실제 적용하고 249에 의존하지 않는 검증된 Linux DHCP guest이며')
 else:
  s=s.replace('API 응답 확인만으로', 'Neutron이 빈값을 지원하지 않는 249는 요청하지 않으며 검증된 Linux DHCP guest를 지원합니다. API 응답 확인만으로')
 p.write_text(s,encoding='utf8')
p=OUT/'local_probe_test.go';s=p.read_text(encoding='utf8');start=s.index('func localAptCommand');s=s[:start]+s[start:].replace("\n print(json.dumps({'step':'route-added'}),flush=True)","\nprint(json.dumps({'step':'route-added'}),flush=True)",1);p.write_text(s,encoding='utf8')
# Preserve the generator while avoiding the overbroad replacement that inserted an indent in apt.
p=OUT/'update_diagnostic.py';s=p.read_text(encoding='utf8').replace('"subprocess.run([\'sudo\',\'ip\',\'route\',\'add\']+args,check=True,stdout=subprocess.DEVNULL)"','" subprocess.run([\'sudo\',\'ip\',\'route\',\'add\']+args,check=True,stdout=subprocess.DEVNULL)"').replace('"subprocess.run([\'sudo\',\'-n\',\'ip\',\'route\',\'add\']+args,check=True,stdout=subprocess.DEVNULL)\\n print','" subprocess.run([\'sudo\',\'-n\',\'ip\',\'route\',\'add\']+args,check=True,stdout=subprocess.DEVNULL)\\n print');p.write_text(s,encoding='utf8')
sp.run([r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\gofmt.exe','-w',*[str(base/n) for n in ['management_network.go','management_network_test.go','management_subnet_affinity_test.go']],str(OUT/'local_probe_test.go')],check=True)
print('canonical API policy and helper syntax fixed; initial failure preserved')
