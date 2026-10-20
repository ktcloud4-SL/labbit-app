import json,datetime,http.client,urllib.parse,uuid,re
HOST='jinwook.store'
results={'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'transport':'localhost kubectl port-forward; production backend and connector','bodiesCredentialsPersisted':False,'cases':{}}
cookies={}
def call(label,host,path,method='GET',data=None,cookie='',origin=None):
    headers={'Host':host,'Origin':origin or 'https://'+host,'Content-Type':'application/json'}
    if cookie:headers['Cookie']=cookie
    body=None if data is None else json.dumps(data).encode()
    conn=http.client.HTTPConnection('127.0.0.1',18080,timeout=40)
    try:
        conn.request(method,path,body,headers);response=conn.getresponse();raw=response.read();hs=response.getheaders()
        try:value=json.loads(raw)
        except Exception:value={}
        code=value.get('code') if isinstance(value,dict) else None
        results['cases'][label]={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':response.status,'code':code if isinstance(code,str) and re.fullmatch('[a-z_]{1,80}',code) else None}
        return value,hs,raw
    finally:conn.close()
for role in ['student','instructor','admin']:
    _,headers,_=call('login_'+role,HOST,'/api/v1/auth/login','POST',{'username':'dev-'+role,'password':PASSWORD})
    cookies[role]='; '.join(v.split(';',1)[0] for k,v in headers if k.lower()=='set-cookie')
    assert results['cases']['login_'+role]['status']==204
student='ee63c707-3ef9-4492-ae74-5eab90474694';teacher='458d4656-6983-4e93-bf59-42afa7ba1d4d'
for label,role,target,port in [('student_other_workspace','student',teacher,3000),('instructor_other_workspace','instructor',student,3000),('admin_other_workspace','admin',student,3000),('ssh_port','student',student,22),('nonallowed_port','student',student,3001)]:
    call(label,HOST,'/api/v1/lab-instances/'+target+'/preview-sessions','POST',{'targetPort':port},cookies[role])
sessions=[]
try:
    for label,role,target in [('student_session','student',student),('teacher_session','instructor',teacher)]:
        value,headers,_=call(label,HOST,'/api/v1/lab-instances/'+target+'/preview-sessions','POST',{'targetPort':3000},cookies[role])
        assert results['cases'][label]['status']==201
        sid=str(uuid.UUID(value['id']));sessions.append((sid,role));parsed=urllib.parse.urlsplit(value['previewUrl']);ph=parsed.hostname
        results[label]={'id':sid,'expiresAt':value['expiresAt'],'separateOriginSameSite':parsed.scheme=='https' and ph!=HOST and ph.endswith('.'+HOST),'noStore':any(k.lower()=='cache-control' and v=='no-store' for k,v in headers)}
        call(label+'_anonymous',ph,'/')
        call(label+'_saas_cookie_only',ph,'/',cookie=cookies[role])
        call(label+'_wrong_origin_exchange',ph,'/__labbit/exchange','POST',{'credential':parsed.fragment},origin='https://'+HOST)
        _,exchange_headers,_=call(label+'_exchange',ph,'/__labbit/exchange','POST',{'credential':parsed.fragment})
        pc='; '.join(v.split(';',1)[0] for k,v in exchange_headers if k.lower()=='set-cookie')
        results[label]['cookieSecureHostOnlyHttpOnly']=any(k.lower()=='set-cookie' and 'Secure' in v and 'HttpOnly' in v and 'Domain=' not in v for k,v in exchange_headers)
        call(label+'_replay_exchange',ph,'/__labbit/exchange','POST',{'credential':parsed.fragment})
        _,_,raw=call(label+'_get',ph,'/',cookie=pc)
        results[label]['appGetMarker']=b'Labbit C3' in raw
        other='instructor' if role=='student' else 'student'
        call(label+'_other_close',HOST,'/api/v1/preview-sessions/'+sid,'DELETE',cookie=cookies[other])
        call(label+'_admin_close',HOST,'/api/v1/preview-sessions/'+sid,'DELETE',cookie=cookies['admin'])
        _,_,raw=call(label+'_still_active',ph,'/',cookie=pc)
        results[label]['unauthorizedCloseDidNotInvalidate']=b'Labbit C3' in raw
        if role=='student':first_host,first_cookie=ph,pc
        else:
            call('cross_session_cookie',ph,'/',cookie=first_cookie)
            call('first_session_still_active',first_host,'/',cookie=first_cookie)
        if role=='instructor':
            call(label+'_close',HOST,'/api/v1/preview-sessions/'+sid,'DELETE',cookie=cookies[role])
            call(label+'_after_close',ph,'/',cookie=pc)
            call(label+'_close_repeat',HOST,'/api/v1/preview-sessions/'+sid,'DELETE',cookie=cookies[role])
            call('student_survives_teacher_close',first_host,'/',cookie=first_cookie)
    sid,role=sessions[0]
    call('student_session_close',HOST,'/api/v1/preview-sessions/'+sid,'DELETE',cookie=cookies[role])
    call('student_session_after_close',first_host,'/',cookie=first_cookie)
    call('student_session_close_repeat',HOST,'/api/v1/preview-sessions/'+sid,'DELETE',cookie=cookies[role])
finally:
    for sid,role in sessions:
        call('cleanup_'+role,HOST,'/api/v1/preview-sessions/'+sid,'DELETE',cookie=cookies[role])
print(json.dumps(results))
