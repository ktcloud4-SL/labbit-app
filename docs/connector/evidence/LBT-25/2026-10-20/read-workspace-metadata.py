import urllib.request,json,datetime
a=PRIVATE['auth'];base='https://api.ucloudbiz.olleh.com/d1'
body={'auth':{'identity':{'methods':['password'],'password':{'user':{'name':a['username'],'password':a['password'],'domain':{'id':a.get('user_domain_id','default')}}}},'scope':{'project':{'id':a['project_id']}}}}
with urllib.request.urlopen(urllib.request.Request(base+'/identity/auth/tokens',data=json.dumps(body).encode(),headers={'Content-Type':'application/json'}),timeout=15) as r:
    token=r.headers['X-Subject-Token'];project=json.load(r)['token']['project']['id']
assert project==PRIVATE['projectId']=='63c7a8bdc0c647dcb85ba1c4a1899976'
result={'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'servers':[],'readOnlyResourceAPI':True}
for instance,server in [('ee63c707-3ef9-4492-ae74-5eab90474694','27412c58-60bd-4b37-9bbd-78365be6681d'),('458d4656-6983-4e93-bf59-42afa7ba1d4d','27725801-32c3-4c2b-b8f9-d2ea00e093a8')]:
    with urllib.request.urlopen(urllib.request.Request(base+'/server/servers/'+server,headers={'X-Auth-Token':token}),timeout=15) as r:s=json.load(r)['server']
    assert s['id']==server and s['tenant_id']==project
    m=s['metadata']
    result['servers'].append({'instanceId':instance,'serverId':server,'status':s['status'],'vmKeyIsVm1':m.get('labbit_vm_key')=='vm-1','workspaceDesignationIsVm1':m.get('labbit_workspace_vm_key')=='vm-1','workspaceDesignationPresent':'labbit_workspace_vm_key' in m,'instanceAffinityMatches':m.get('labbit_lab_instance_id')==instance})
print(json.dumps(result))
