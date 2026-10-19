import hashlib, json, subprocess as sp, zipfile
from pathlib import Path
base=Path('C:/Users/user/.cache/labbit-lbt174-pg16')
archive=base/'postgresql-16-windows.zip'
print(json.dumps({'url':'https://sbp.enterprisedb.com/getfile.jsp?fileid=1260623','sha256':hashlib.sha256(archive.read_bytes()).hexdigest(),'bytes':archive.stat().st_size}),flush=True)
with zipfile.ZipFile(archive) as z:
    selected=[n for n in z.namelist() if n.startswith(('pgsql/bin/','pgsql/lib/','pgsql/share/'))]
    for n in selected:
        assert (base/n).resolve().is_relative_to(base.resolve())
    z.extractall(base,selected)
bin=base/'pgsql/bin'
sp.run([str(bin/'postgres.exe'),'--version'],check=True)
data=base/'data'
if not data.exists():
    sp.run([str(bin/'initdb.exe'),'-D',str(data),'-U','postgres','-A','trust','--encoding=UTF8','--locale=C'],check=True)
sp.run([str(bin/'pg_ctl.exe'),'-D',str(data),'-l',str(base/'postgres.log'),'-o','-h 127.0.0.1 -p 15432','-w','start'],check=True)
sp.run([str(bin/'psql.exe'),'-h','127.0.0.1','-p','15432','-U','postgres','-d','postgres','-c','SELECT version();'],check=True)
