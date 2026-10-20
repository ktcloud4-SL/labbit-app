import json,datetime,urllib.request,urllib.error,http.cookiejar,uuid,re
origin='https://jinwook.store'
base=globals().get('BASE_URL',origin)
jar=http.cookiejar.CookieJar()
client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
def request(path,method='GET',data=None):
    body=None if data is None else json.dumps(data).encode()
    headers={'Origin':origin,'Host':'jinwook.store','Content-Type':'application/json','Accept':'application/json'}
    if base!=origin and list(jar):headers['Cookie']='; '.join(c.name+'='+c.value for c in jar)
    req=urllib.request.Request(base+path,data=body,method=method,headers=headers)
    try:r=client.open(req,timeout=40)
    except urllib.error.HTTPError as e:r=e
    raw=r.read()
    try:value=json.loads(raw)
    except Exception:value={}
    code=value.get('code')
    meta={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':r.code,'contentType':r.headers.get('Content-Type'),'code':code if isinstance(code,str) and re.fullmatch('[a-z_]{1,80}',code) else None,'bodyBytes':len(raw)}
    for key in ['X-Request-ID','X-Correlation-ID']:
        try:meta[key]=str(uuid.UUID(r.headers.get(key)))
        except Exception:pass
    return meta,value
login,_=request('/api/v1/auth/login','POST',{'username':'dev-student','password':PASSWORD})
result={'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'login':login,'bodyCookieCredentialPersisted':False}
if login['status'] in [200,204]:
    opened,value=request('/api/v1/lab-instances/ee63c707-3ef9-4492-ae74-5eab90474694/preview-sessions','POST',{'targetPort':3000})
    result['create']=opened
    if opened['status']==201:
        session=str(uuid.UUID(value['id']));result['sessionId']=session
        result['cleanup'],_=request('/api/v1/preview-sessions/'+session,'DELETE')
print(json.dumps(result))
