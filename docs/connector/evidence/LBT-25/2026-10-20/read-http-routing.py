import subprocess,json,datetime,urllib.request,urllib.error,re
result={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'requests':[]}
services=json.loads(subprocess.check_output(['kubectl','-n','labbit','get','services','-o','json'],text=True))['items']
result['services']=[{'name':s['metadata']['name'],'ports':[p['port'] for p in s['spec']['ports']]} for s in services]
for path in ['/healthz','/api/v1/auth/me']:
    try:r=urllib.request.urlopen('https://jinwook.store'+path,timeout=15)
    except urllib.error.HTTPError as e:r=e
    data=r.read()
    try:value=json.loads(data)
    except Exception:value={}
    code=value.get('code')
    result['requests'].append({'status':r.code,'contentType':r.headers.get('Content-Type'),'server':r.headers.get('Server'),'code':code if isinstance(code,str) and re.fullmatch('[a-z_]{1,80}',code) else None,'jsonKeys':list(value) if isinstance(value,dict) else None,'bytes':len(data)})
print(json.dumps(result))
