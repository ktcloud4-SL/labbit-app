"""Verify inherited cloud opt-in suppression without external API calls."""
from pathlib import Path
import json
import os

folder=Path(__file__).resolve().parent
namespace={'__name__':'evidence_env_regression','__file__':str(folder/'validate.py')}
exec(compile((folder/'validate.py').read_text(encoding='utf-8'),str(folder/'validate.py'),'exec'),namespace)
flags=['LABBIT_OPENSTACK_INTEGRATION','LABBIT_OPENSTACK_MUTATION_TEST','LABBIT_OPENSTACK_M2_TEST','LABBIT_OPENSTACK_M3_TEST','LABBIT_OPENSTACK_M2_WSS_TEST','LABBIT_OPENSTACK_M3_WSS_TEST','LABBIT_OPENSTACK_INTERNET_POLICY_TEST','LABBIT_OPENSTACK_VM_ACCESS_TEST','LABBIT_OPENSTACK_PTY_CC02_TEST','LABBIT_OPENSTACK_RESIDUAL_AUDIT','LABBIT_KTCLOUD_D1_INTEGRATION','LABBIT_KTCLOUD_D1_NETWORK_MUTATION_TEST','LABBIT_KTCLOUD_FUTURE_AUDIT']
original=os.environ.copy()
captured=[]
try:
    os.environ.update({key:'1' for key in flags})
    os.environ.update(OS_CLOUD='synthetic-no-api',LABBIT_PROVIDER_CONFIG_FILE='synthetic-no-api',LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE='synthetic-no-api')
    env=namespace['clean_env']()
    assert all(env[key]=='0' for key in flags),'ordinary automation inherited a cloud opt-in'
    def fake_run(name, command, **kwargs):
        active={key for key in flags if kwargs['env'].get(key)=='1'}
        assert active=={'LABBIT_OPENSTACK_INTEGRATION','LABBIT_OPENSTACK_PTY_CC02_TEST'},active
        captured.append(name)
        return 0
    namespace['run']=fake_run
    namespace['cloud']()
    assert captured==['pre-discovery','real-pty-cc02','post-discovery'],captured
    record={'exitCode':0,'inheritedFlagsSuppressed':len(flags),'ordinaryAutoActiveCloudFlags':0,'explicitCloudActiveFlags':['LABBIT_OPENSTACK_INTEGRATION','LABBIT_OPENSTACK_PTY_CC02_TEST'],'commandsIntercepted':captured,'actualCloudCommandsOrAPIsExecuted':0}
    namespace['save']('runner-env-regression-run.json',record)
    print(json.dumps(record))
finally:
    os.environ.clear()
    os.environ.update(original)
