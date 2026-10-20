import pathlib,subprocess,json,datetime,os
pods=json.loads(subprocess.check_output(['kubectl','-n','labbit','get','pods','-o','json'],timeout=40))['items']
images=[{'pod':p['metadata']['name'],'images':[c['image'] for c in p['spec']['containers']]} for p in pods if p['metadata']['name'].startswith(('backend-','web-'))]
forward=False
for p in pathlib.Path('/proc').iterdir():
    if not p.name.isdecimal():continue
    try:
        args=(p/'cmdline').read_bytes().split(b'\0')
        if args and os.path.basename(args[0])==b'kubectl' and b'port-forward' in args and b'18080:8080' in args:forward=True
    except (OSError,PermissionError):pass
print(json.dumps({'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'images':images,'temporaryPortForwardStillRunning':forward,'cloudMutationsPerformedByThisCollector':False}))
