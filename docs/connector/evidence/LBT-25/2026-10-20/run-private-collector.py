"""Cloud credentials stay on the local operator host; this collector never uses SSH."""
import pathlib,subprocess,sys,json,yaml
from run_output import reserve_output

COLLECTOR=pathlib.Path(__file__).resolve().with_name('read-workspace-metadata.py')


def minimal_inputs(auth,ledger):
    return {'auth':{'username':auth['username'],'password':auth['password'],
                    'user_domain_id':auth.get('user_domain_id','default'),
                    'project_id':auth['project_id']},'projectId':ledger['projectId']}


def read_private():
    folder=pathlib.Path.home()/'.config/labbit/c3-connector-20261019'
    auth=yaml.safe_load((pathlib.Path.home()/'.config/openstack/ktcloud-d1/clouds.yaml').read_text(encoding='utf-8-sig'))['clouds']['ktcloud-d1']['auth']
    ledger=json.loads((folder/'ktcloud-bootstrap-ledger.json').read_text(encoding='utf-8-sig'))
    return minimal_inputs(auth,ledger)


def collect(private):
    script=COLLECTOR.read_text(encoding='utf-8-sig')
    source='PRIVATE='+repr(private)+'\nimport json,sys\ntry:\n exec('+repr(script)+')\nexcept Exception as e:\n print(json.dumps({"collectorError":type(e).__name__,"httpStatus":getattr(e,"code",None) if isinstance(getattr(e,"code",None),int) else None}));sys.exit(1)\n'
    # stdin only, to the same local Python executable: no remote host, env or argv secret.
    return subprocess.run([sys.executable,'-B','-'],input=source.encode(),capture_output=True,timeout=90)


def main():
    output=reserve_output('workspace-metadata.json')
    try:
        if len(sys.argv)>2 or (len(sys.argv)==2 and pathlib.Path(sys.argv[1]).resolve()!=COLLECTOR):
            raise ValueError('only the bundled metadata collector is supported')
        result=collect(read_private())
        output.write_bytes(result.stdout)
        sys.stdout.buffer.write(result.stdout)
        if result.returncode:print('collectorExit',result.returncode,'stderrBytes',len(result.stderr))
        return result.returncode
    except Exception as error:
        # YAML/HTTP/timeout exception text can contain protected source excerpts.
        safe=json.dumps({'collectorError':type(error).__name__})+'\n'
        output.write_text(safe,encoding='utf-8')
        print(safe,end='')
        return 1


if __name__=='__main__':
    raise SystemExit(main())
