import pathlib,subprocess,os,sys
from run_output import reserve_output
output = reserve_output('api-matrix.json')
folder=pathlib.Path(__file__).parent
setup="import subprocess,json,socket,time\ncheck=socket.socket();check.settimeout(1);assert check.connect_ex(('127.0.0.1',18080))!=0;check.close()\npf=subprocess.Popen(['kubectl','-n','labbit','port-forward','service/backend','18080:8080','--address=127.0.0.1'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)\n"
ready="for i in range(100):\n assert pf.poll() is None\n sock=socket.socket();sock.settimeout(.1);ready=sock.connect_ex(('127.0.0.1',18080))==0;sock.close()\n if ready:break\n time.sleep(.1)\nassert ready\n"
program=ready+(folder/'api-matrix.py').read_text(encoding='utf-8')
source='PASSWORD='+repr(os.environ['C3_PASSWORD'])+'\n'+setup+'try:\n exec('+repr(program)+')\nfinally:\n pf.terminate()\n pf.wait(timeout=5)\n'
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=2','admin@172.16.8.10','python3 -'],input=source.encode(),capture_output=True,timeout=180)
output.write_bytes(r.stdout)
sys.stdout.buffer.write(r.stdout)
if r.returncode:print('probeExit',r.returncode,'stderrBytes',len(r.stderr))
raise SystemExit(r.returncode)
