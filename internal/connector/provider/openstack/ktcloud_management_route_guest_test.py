"""Linux fixture tests. No host network, Netplan, or cloud mutation is run."""
import json,os,pathlib,subprocess,tempfile,types,unittest
from unittest.mock import patch

HERE=pathlib.Path(__file__).resolve().parent

class ManagementRouteDiagnosticsTest(unittest.TestCase):
    def test_failure_phase_and_kind_without_command_or_output(self):
        ns={}
        exec((HERE/'ktcloud_management_route.py').read_text(),ns)
        cases=[(['netplan','generate'],subprocess.CalledProcessError(78,['secret'],output='secret',stderr='secret'),'netplan generate CalledProcessError 78'),
               (['systemctl','reload','systemd-networkd'],subprocess.TimeoutExpired(['secret'],20,output='secret'),'systemctl reload TimeoutExpired '),
               (['ip','-j','address'],FileNotFoundError('secret'),'ip -j FileNotFoundError ')]
        for args,error,expected in cases:
            with self.subTest(expected=expected),patch.object(ns['subprocess'],'check_output',side_effect=error):
                with self.assertRaises(type(error)) as caught:ns['run'](args)
                message=ns['failure'](caught.exception)
                self.assertEqual(message,'management route: '+expected)
                self.assertNotIn('secret',message)

@unittest.skipUnless(os.name=='posix' and os.geteuid()==0,'requires Linux root in an isolated fixture directory')
class ManagementRouteTest(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory()
        self.root=pathlib.Path(self.temp.name)
        self.addCleanup(self.temp.cleanup)
        self.overlay=self.root/'etc/netplan/90-labbit-management-ssh.yaml'
        self.overlay.parent.mkdir(parents=True)
        self.original=self.overlay.with_name('50-cloud-init.yaml')
        self.original.write_bytes(b'network: original DHCP configuration\n')
        self.generated=self.root/'run/systemd/network/10-netplan-ens9.network'
        self.generated.parent.mkdir(parents=True)
        self.generated.write_text('[Match]\nName=ens9\n[Route]\nDestination=172.25.0.11/32\nGateway=172.25.241.1\nMetric=5\n')
        self.calls=[]
        self.addresses=[{'ifname':'ens9','addr_info':[{'family':'inet','local':'172.25.241.8'}]}]
        self.active={'Name':'ens9','NetworkFile':'/run/systemd/network/10-netplan-ens9.network','NetworkFileDropins':[]}
        self.exact=[{'dev':'ens9','gateway':'172.25.241.1','metric':5,'protocol':'static'}]
        self.selected=[{'dev':'ens9','gateway':'172.25.241.1'}]
        self.ns={}
        exec((HERE/'ktcloud_management_route.py').read_text()+(HERE/'ktcloud_management_route_verify.py').read_text(),self.ns)
        self.ns.update(C={'management':'172.25.241.0/24','source':'172.25.0.11'},VERIFY=False,run=self.run_command)
        def mapped_path(value):
            value=pathlib.Path(value)
            if value.is_absolute():return self.root/str(value).lstrip('/')
            return value
        self.ns['pathlib']=types.SimpleNamespace(Path=mapped_path)

    def run_command(self,args):
        self.calls.append(args)
        if args==['ip','-j','address']:return json.dumps(self.addresses)
        if args[:2]==['networkctl','status']:return json.dumps(self.active)
        if args[:5]==['ip','-j','route','show','exact']:return json.dumps(self.exact)
        if args[:4]==['ip','-j','route','get']:return json.dumps(self.selected)
        if args in (['netplan','generate'],['systemctl','reload','systemd-networkd']):return ''
        raise AssertionError(args)

    def prepare(self):return self.ns['prepare']()
    def verify(self):
        self.ns['VERIFY']=True
        self.ns['verify'](*self.prepare())

    def test_persistent_route_preserves_original_and_dynamic_address(self):
        self.assertEqual(self.prepare(),('ens9','172.25.241.8','172.25.241.1'))
        before=self.overlay.read_bytes()
        self.assertEqual(self.original.read_bytes(),b'network: original DHCP configuration\n')
        self.assertEqual(self.overlay.stat().st_mode&0o777,0o600)
        self.assertIn(['netplan','generate'],self.calls)
        self.assertIn(['systemctl','reload','systemd-networkd'],self.calls)
        self.addresses[0]['addr_info'][0]['local']='172.25.241.19'
        self.verify()
        self.assertEqual(self.overlay.read_bytes(),before)

    def test_missing_or_ambiguous_management_interface_rejected(self):
        for addresses in ([],self.addresses+self.addresses):
            self.addresses=addresses
            with self.assertRaises(ValueError):self.prepare()
            self.assertFalse(self.overlay.exists())

    def test_foreign_overlay_and_symlink_are_never_overwritten(self):
        self.overlay.write_bytes(b'foreign')
        with self.assertRaises(ValueError):self.prepare()
        self.assertEqual(self.overlay.read_bytes(),b'foreign')
        self.overlay.unlink();self.overlay.symlink_to(self.original)
        with self.assertRaises(ValueError):self.prepare()
        self.assertEqual(self.original.read_bytes(),b'network: original DHCP configuration\n')

    def test_verify_is_readonly_and_rejects_lucky_default_route(self):
        self.prepare();self.calls.clear();self.verify()
        self.assertFalse(any(c in (['netplan','generate'],['systemctl','reload','systemd-networkd']) for c in self.calls))
        self.exact=[]
        with self.assertRaises(ValueError):self.verify()

    def test_inactive_generated_file_or_dropin_cannot_prove_persistence(self):
        self.prepare()
        self.active['NetworkFile']='/etc/systemd/network/05-custom.network'
        with self.assertRaises(ValueError):self.verify()
        self.active['NetworkFile']='/run/systemd/network/10-netplan-ens9.network'
        self.active['NetworkFileDropins']=['/etc/systemd/network/10-netplan-ens9.network.d/override.conf']
        with self.assertRaises(ValueError):self.verify()

    def test_wrong_return_path_or_protocol_rejected(self):
        self.prepare();self.selected=[{'dev':'ens10','gateway':'172.25.240.1'}]
        with self.assertRaises(ValueError):self.verify()
        self.selected=[{'dev':'ens9','gateway':'172.25.241.1'}];self.exact[0]['protocol']='dhcp'
        with self.assertRaises(ValueError):self.verify()

    def test_netplan_or_reload_failure_is_not_success(self):
        for failure in (['netplan','generate'],['systemctl','reload','systemd-networkd']):
            original=self.ns['run']
            def failed(args):
                if args==failure:raise RuntimeError('fixture failure')
                return self.run_command(args)
            self.ns['run']=failed
            with self.assertRaises(RuntimeError):self.prepare()
            self.ns['run']=original

    def test_filesystem_failure_is_not_misattributed_to_last_command(self):
        self.original.unlink();self.overlay.parent.rmdir()
        with self.assertRaises(FileNotFoundError) as caught:self.prepare()
        self.assertTrue(self.ns['failure'](caught.exception).startswith('management route: overlay FileNotFoundError'))

    def test_active_config_read_failure_has_own_phase(self):
        self.prepare();self.generated.unlink()
        with self.assertRaises(FileNotFoundError) as caught:self.verify()
        self.assertTrue(self.ns['failure'](caught.exception).startswith('management route: active-config FileNotFoundError'))

if __name__=='__main__':unittest.main()
