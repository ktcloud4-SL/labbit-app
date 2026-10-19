import hashlib
import json
from pathlib import Path
import subprocess

out=Path(__file__).resolve().parent
repo=out.parent/'labbit-connector-gates-20261015'
root=out.parent/'lbt174-followup-20261016'
tested=json.loads((root/'sealed/source-manifest.json').read_text(encoding='utf-8'))
reviewed=json.loads((root/'finalized/source-manifest.json').read_text(encoding='utf-8'))
author=json.loads((root/'finalized/postgres-source-bridge.json').read_text(encoding='utf-8'))
command=['go','list','-deps','-test','-json','-tags=integration','./internal/postgres','./internal/postgres/postgrestest','./internal/server/app','./internal/server/auth','./internal/server/httpapi']
raw=subprocess.check_output(command,cwd=repo).decode()
decoder=json.JSONDecoder()
paths={'go.mod','go.sum'}
position=0
while position<len(raw):
    while position<len(raw) and raw[position].isspace(): position+=1
    if position==len(raw): break
    package,end=decoder.raw_decode(raw,position)
    position=end
    if 'Dir' not in package: continue
    directory=Path(package['Dir'])
    if not directory.is_relative_to(repo): continue
    # Synthetic ForTest packages put their actually compiled tests in GoFiles.
    # TestGoFiles on ordinary imported packages lists eligible, uncompiled tests.
    for key in ('GoFiles','CgoFiles'):
        for name in package.get(key,[]):
            path=directory/name
            if path.exists() and path.is_relative_to(repo): paths.add(path.relative_to(repo).as_posix())
different=[p for p in sorted(paths) if tested['files'].get(p)!=reviewed['files'].get(p)]
global_different=[p for p in sorted(set(tested['files'])|set(reviewed['files'])) if tested['files'].get(p)!=reviewed['files'].get(p)]
report={'command':command,'tested':tested['sourceSHA256'],'reviewed':reviewed['sourceSHA256'],'compiled_repo_go_module_files':len(paths),'author_set_difference':sorted(paths^set(author['compiledRepoFiles'])),'compiled_differences':different,'global_differences':global_different,'unchanged_contract_migration_runtime_inputs':'Independently checked by check_finalized_source.py against HEAD; this closure is Go/module inputs only. Original sourceUnchanged=false result remains preserved.'}
(out/'postgres-bridge-independent-check.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(report,ensure_ascii=False,indent=2))
assert not different and not report['author_set_difference']
