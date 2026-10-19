from pathlib import Path
import datetime,json,subprocess as sp
OUT=Path(__file__).resolve().parent;ROOT=OUT.parent/'labbit-connector-gates-20261015'
GO=r'C:\Users\user\AppData\Local\Programs\Go-1.27.1\go\bin\go.exe'
packages=sp.check_output([GO,'list','./...'],cwd=ROOT).decode().splitlines()
value={'observedUTC':datetime.datetime.now(datetime.timezone.utc).isoformat(),'phase':'before new Evidence package export','command':['go','list','./...'],'packages':packages,'count':len(packages)}
(OUT/'go-package-scope-before.json').write_text(json.dumps(value,indent=2)+'\n',encoding='utf8')
print(json.dumps({'packageCount':len(packages)}))
