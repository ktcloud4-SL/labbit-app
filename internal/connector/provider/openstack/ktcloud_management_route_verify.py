def verify(device,address,gateway):
    global PHASE
    active=json.loads(run(['networkctl','status','--json=short',device]))
    require(active.get('Name')==device and not active.get('NetworkFileDropins'))
    PHASE='active-config'
    file=pathlib.Path(active['NetworkFile'])
    require(file.parent==pathlib.Path('/run/systemd/network') and file.match('*netplan*.network'))
    sections=file.read_text().split('[')
    require(any(s.startswith('Match]') and 'Name='+device in s.splitlines() for s in sections))
    require(any(s.startswith('Route]') and all(x in s.splitlines() for x in ['Destination='+C['source']+'/32','Gateway='+gateway,'Metric=5']) for s in sections))
    exact=json.loads(run(['ip','-j','route','show','exact',C['source']+'/32']))
    require(any(r.get('dev')==device and r.get('gateway')==gateway and r.get('metric')==5 and r.get('protocol')=='static' for r in exact))
    route=json.loads(run(['ip','-j','route','get',C['source'],'from',address]))
    require(len(route)==1 and route[0].get('dev')==device and route[0].get('gateway')==gateway)
