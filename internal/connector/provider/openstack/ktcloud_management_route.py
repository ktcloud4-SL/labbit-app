import ipaddress,json,os,pathlib,stat,subprocess

PHASE='bootstrap'

def require(condition):
    global PHASE
    if not condition:
        PHASE='validation'
        raise ValueError()

def run(args):
    global PHASE
    PHASE=' '.join(args[:2])
    return subprocess.check_output(args,stderr=subprocess.DEVNULL,text=True,timeout=20)

def failure(error):
    return 'management route: '+PHASE+' '+type(error).__name__+' '+str(getattr(error,'returncode',''))

def prepare():
    global PHASE
    require(os.geteuid()==0)
    network=ipaddress.ip_network(C['management'])
    require(network.version==4 and network.prefixlen==24)
    source=ipaddress.ip_address(C['source'])
    require(source.version==4 and source not in network)
    gateway=str(network.network_address+1)
    interfaces=json.loads(run(['ip','-j','address']))
    matches=[(n['ifname'],a['local']) for n in interfaces for a in n.get('addr_info',[]) if a.get('family')=='inet' and ipaddress.ip_address(a['local']) in network]
    require(len(matches)==1)
    device,address=matches[0]
    PHASE='overlay'
    data=(json.dumps({'network':{'version':2,'ethernets':{device:{'routes':[{'to':str(source)+'/32','via':gateway,'metric':5}]}}}},sort_keys=True)+'\n').encode()
    path=pathlib.Path('/etc/netplan/90-labbit-management-ssh.yaml')
    if not VERIFY and not path.exists() and not path.is_symlink():
        fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
        with os.fdopen(fd,'wb') as output:
            output.write(data);output.flush();os.fsync(output.fileno())
        fd=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY);os.fsync(fd);os.close(fd)
    info=path.lstat()
    require(stat.S_ISREG(info.st_mode) and info.st_uid==0 and stat.S_IMODE(info.st_mode)==0o600 and path.read_bytes()==data)
    if not VERIFY:
        run(['netplan','generate'])
        run(['systemctl','reload','systemd-networkd'])
    return device,address,gateway
