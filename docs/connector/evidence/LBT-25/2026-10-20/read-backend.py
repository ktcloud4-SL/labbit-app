import subprocess,json,datetime,uuid,re
def kubectl(*args):return subprocess.check_output(['kubectl','-n','labbit',*args],text=True,timeout=40)
pods=json.loads(kubectl('get','pods','-l','app=backend','-o','json'))['items']
if not pods:
    pods=[p for p in json.loads(kubectl('get','pods','-o','json'))['items'] if p['metadata']['name'].startswith('backend-')]
safeMessages={
'PreviewSession 생성 실패':'create_failed',
'PreviewSession 생성 취소':'create_canceled',
'Connector PREVIEW_OPEN 실패 보고':'connector_open_failed',
'tunnel 준비 실패':'tunnel_failed',
'tunnel 준비 시간 초과':'tunnel_timeout',
'tunnel 준비 취소':'tunnel_canceled',
'PreviewSession 생성':'created',
'Connector PREVIEW_OPEN 전송 안 함':'open_not_sent',
'Connector PREVIEW_OPEN 전송':'open_sent',
'Connector PREVIEW_CLOSE 전송 안 함':'close_not_sent',
'Connector PREVIEW_CLOSE 전송':'close_sent',
'Connector Preview message를 pending에 연결하지 못함':'unmatched_result',
'Preview bootstrap 교환 거절':'bootstrap_rejected',
'Preview bootstrap 교환':'bootstrap_exchanged',
'Preview 요청 실패':'request_failed',
'Connector Preview Data 인증 거절':'data_auth_rejected',
'Connector Preview Data subprotocol 거절':'data_subprotocol_rejected',
'Connector Preview Data 인증 의존성 오류':'data_auth_dependency_error',
'Connector Preview Data Upgrade 실패':'data_upgrade_failed',
'Connector Preview Data attach 수신 실패':'attach_receive_failed',
'Connector Preview Data protocol 위반':'data_protocol_violation',
'Connector Preview Data attach 거절':'attach_rejected',
'Connector Preview Data attach':'data_attached',
'Connector Preview Data 전송 실패':'data_send_failed',
'PreviewSession 활성화':'activated',
'PreviewSession 종료':'closed'}
safeValues={'missing_or_malformed_authorization','unsupported_subprotocol','unauthenticated','upgrade_failed','binary_before_attach','invalid_attach','invalid_attach_payload','attached_write_failed','DEPENDENCY_UNAVAILABLE','BOOTSTRAP_REJECTED','TUNNEL_CLOSED','UPSTREAM_TIMEOUT','UPSTREAM_ERROR','APP_NOT_RUNNING','PORT_REJECTED','VM_UNREACHABLE','UNAVAILABLE','INTERNAL_ERROR','not_connected','not_ready','connection_closing'}
safeValues.update({'capability_unsupported','connector_unavailable','open_send_failed','control_not_bound','ended_before_attach','open_timeout','request_canceled','activate_failed','reset_race','recheck_failed'})
safeValues.update({'SESSION_CLOSED','SESSION_EXPIRED','LAB_RESET','LAB_CLEANUP','SERVICE_RESTARTING','PROTOCOL_VIOLATION','CREDENTIAL_REVOKED','CONTROL_REPLACED'})
result={'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'pods':[],'events':[],'unrecognizedPreviewLogMessages':0,'rawLogPersisted':False}
for p in pods:
    item={'name':p['metadata']['name'],'phase':p['status']['phase'],'containers':[]}
    for c in p['spec']['containers']:
        env={e['name']:e.get('value','<reference>') for e in c.get('env',[]) if e['name'] in ['LABBIT_RUNTIME_ROLES','LABBIT_PREVIEW_ALLOWED_PORTS','LABBIT_PREVIEW_SESSION_TTL','LABBIT_PREVIEW_ORIGIN_TEMPLATE','LABBIT_PUBLIC_ORIGIN']}
        item['containers'].append({'name':c['name'],'image':c['image'],'settings':env})
        log=kubectl('logs',p['metadata']['name'],'-c',c['name'],'--since-time=2026-10-20T05:32:00Z','--timestamps=true')
        for line in log.splitlines():
            try:q=json.loads(line[line.index('{'):])
            except (ValueError,json.JSONDecodeError):continue
            rows=[q]+[q[k] for k in ['fields','attrs','attributes'] if isinstance(q.get(k),dict)]
            event={}
            for row in rows:
                for k in ['msg','message']:
                    if row.get(k) in safeMessages:event['event']=safeMessages[row[k]]
                for k in ['preview_session_id','lab_instance_id','connector_id','request_id']:
                    try:event[k]=str(uuid.UUID(row[k]))
                    except (KeyError,ValueError,TypeError):pass
                for k in ['reason','error_code']:
                    if row.get(k) in safeValues:event[k]=row[k]
            if event.get('event'):
                event['at']=line.split(' ',1)[0];result['events'].append(event)
            elif any('Preview' in str(row.get('msg',row.get('message',''))) for row in rows):result['unrecognizedPreviewLogMessages']+=1
    result['pods'].append(item)
print(json.dumps(result))
