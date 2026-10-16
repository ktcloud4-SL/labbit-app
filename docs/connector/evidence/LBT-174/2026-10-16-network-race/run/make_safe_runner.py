from pathlib import Path
import hashlib,json
OUT=Path(__file__).resolve().parent
results=[]
for directory in [OUT,OUT/'linux-v3']:
 original=directory/'run_validation.py'
 source=original.read_text(encoding='utf8')
 source=source.replace('start=time.monotonic()\ntry:',"start=time.monotonic()\nstarted_here=False\nresult['postgresStartedHere']=False\ntry:",1)
 after="stdout=log,stderr=sp.STDOUT,check=True)\n with (OUT/'local-network-race.log')"
 assert after in source
 source=source.replace(after,"stdout=log,stderr=sp.STDOUT,check=True)\n  started_here=True\n  result['postgresStartedHere']=True\n with (OUT/'local-network-race.log')",1)
 source=source.replace('finally:\n',"except BaseException as exc:\n result['runnerExceptionType']=type(exc).__name__\n raise\nfinally:\n",1)
 start=source.index(" with (OUT/'postgres-cleanup.log').open('wb') as log:")
 end=source.index(' result.update(',start)
 block=source[start:end]
 source=source[:start]+" if started_here:\n"+''.join(' '+line for line in block.splitlines(keepends=True))+" else:\n  result['postgresCleanupSkipped']='not_started_here'\n"+source[end:]
 compile(source,str(directory/'run_validation_safe.py'),'exec')
 (directory/'run_validation_safe.py').write_text(source,encoding='utf8')
 results.append({'original':str(original.relative_to(OUT)),'originalSHA256':hashlib.sha256(original.read_bytes()).hexdigest(),'safeRunner':str((directory/'run_validation_safe.py').relative_to(OUT)),'safeRunnerSHA256':hashlib.sha256((directory/'run_validation_safe.py').read_bytes()).hexdigest()})
(OUT/'safe-runner-identity.json').write_text(json.dumps(results,indent=2)+'\n',encoding='utf8')
print('safe reproduction runner created; as-run sources and live process unchanged',flush=True)
