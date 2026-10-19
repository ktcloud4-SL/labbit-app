from pathlib import Path
import runpy,sys,json
OUT=Path(__file__).resolve().parent
ROOT=Path('C:\\Users\\user\\Documents\\ChatGPT\\3차 2\\tmp\\lbt174-routing-postadmin-20261016\\latest-main-reconcile-integration')
sys.argv=['verify.py','load-helpers-only']
v=runpy.run_path(str(OUT/'verify.py'))
if 'race' in __import__('os').environ.get('LBT174_CHECK',''):
 env=v['ENV'].copy();env['CC']=r'C:\Users\user\AppData\Local\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe\mingw64\bin\gcc.exe'
 env['PATH']=str(Path(env['CC']).parent)+__import__('os').pathsep+env['PATH'];env['CGO_ENABLED']='1'
 packages=['./internal/connector/provider/openstack','./internal/connector/vmaccess','./internal/connector/preview','./internal/connector/app','./internal/connector/terminal','./internal/connector/filetransport','./internal/connector/wss','./internal/server/realtime','./internal/server/preview','./tools/ktcloud-d1-acceptance','./tools/ktcloud-op02-bootstrap']
 rc=v['run']('go-race',[str(v['GO']),'test','-race','-p=1','-count=1','-json',*packages],env=env);v['summarize']('go-race');sys.exit(rc)
import yaml
from jsonschema import Draft202012Validator
from openapi_spec_validator import validate
def walk(node,doc):
 if isinstance(node,dict):
  ref=node.get('$ref')
  if isinstance(ref,str) and ref.startswith('#/'):
   current=doc
   for token in ref[2:].split('/'):
    current=current[token.replace('~1','/').replace('~0','~')]
  for value in node.values():walk(value,doc)
 elif isinstance(node,list):
  for value in node:walk(value,doc)
validate(yaml.safe_load((ROOT/'contracts/http/openapi.yaml').read_text(encoding='utf8')))
schemas=list((ROOT/'contracts').rglob('*.json'))
for p in schemas:
 doc=json.loads(p.read_text(encoding='utf8'));assert doc['$schema']==Draft202012Validator.META_SCHEMA['$id'];Draft202012Validator.check_schema(doc);walk(doc,doc)
assert isinstance(yaml.safe_load((ROOT/'runtime/contract.yaml').read_text(encoding='utf8')),dict)
migrations=[p.name.split('_')[0] for p in (ROOT/'db/migrations').glob('[0-9]*_*.sql')];assert len(migrations)==len(set(migrations))
v['save']('contracts-result.json',{'openapi':'PASS','schemas':len(schemas),'runtime':'PASS','migrationMetadata':'PASS','sourceSHA256':v['identity']()['sourceSHA256']})
print('contract validators PASS',flush=True)
