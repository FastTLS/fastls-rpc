"""One bounded Linux check of the installed service dependency graph."""
import concurrent.futures
import http.server
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import threading
import time
import urllib.request

DURATION=300
RSS_GROWTH=64*1024*1024
FD_GROWTH=24


def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1',0))
        return sock.getsockname()[1]


def snapshot(process):
    if process.poll() is not None:
        raise RuntimeError('service exited before recovery check completed')
    root=Path('/proc')/str(process.pid)
    status=dict(line.split(':',1) for line in (root/'status').read_text().splitlines() if ':' in line)
    return {'rss':int(status['VmRSS'].split()[0])*1024,'fds':len(list((root/'fd').iterdir())),'threads':int(status['Threads'])}


def main():
    binary=Path(os.environ['FASTLS_POLICY_BIN_DIR'])
    evidence=Path(os.environ['RELEASE_EVIDENCE'])
    samples=[]
    processes={}
    logs=[]
    started=time.monotonic()
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path=='/slow':
                time.sleep(.25)
            self.send_response(200)
            self.end_headers()
            try: self.wfile.write(b'resource-ok')
            except (BrokenPipeError,ConnectionResetError): pass
        def log_message(self,*args): pass
    server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
    threading.Thread(target=server.serve_forever,daemon=True).start()
    target='http://127.0.0.1:'+str(server.server_port)
    direct=urllib.request.build_opener(urllib.request.ProxyHandler({}))
    result={'status':'failed','duration_seconds':DURATION,'rss_growth_limit_bytes':RSS_GROWTH,'fd_growth_limit':FD_GROWTH,'samples':samples}
    try:
        with tempfile.TemporaryDirectory() as temp:
            temp=Path(temp)
            policy=temp/'policy.json'
            policy.write_text(json.dumps({'allow_hosts':['127.0.0.1'],'allow_cidrs':['127.0.0.1/32']}))
            addresses={name:'127.0.0.1:'+str(port()) for name in ('fastls-fetch','fastls-jsonrpc','fastls-grpc','fastls-mitm')}
            for name,address in addresses.items():
                log=(evidence/(name+'-resource.log')).open('w',encoding='utf-8');logs.append(log)
                args=[str(binary/name),'-addr',address,'-address-policy',str(policy)]
                if name=='fastls-mitm':
                    args+=['-ca-cert',str(temp/'ca.pem'),'-ca-key',str(temp/'key.pem')]
                processes[name]=subprocess.Popen(args,cwd=temp,stdout=log,stderr=subprocess.STDOUT)
            for name,address in addresses.items():
                for attempt in range(100):
                    if processes[name].poll() is not None: raise RuntimeError('startup failed: '+name)
                    try:
                        with socket.create_connection(tuple([address.split(':')[0],int(address.split(':')[1])]),timeout=.1): break
                    except OSError: time.sleep(.1)
                else: raise RuntimeError('startup timed out: '+name)
            # ProxyHandler bypasses loopback; use an explicit absolute-form request.
            def transaction(kind,path='/'):
                if kind=='mitm':
                    import http.client
                    conn=http.client.HTTPConnection(addresses['fastls-mitm'],timeout=3)
                    try:
                        conn.request('GET',target+path)
                        response=conn.getresponse()
                        data=response.read()
                        if response.status!=200 or data!=b'resource-ok': raise RuntimeError('MITM recovery failed')
                    finally: conn.close()
                    return
                params={'url':target+path}
                if kind=='jsonrpc':
                    payload={'jsonrpc':'2.0','method':'fetch','id':1,'params':params}
                    endpoint=addresses['fastls-jsonrpc']+'/rpc'
                else: payload=params;endpoint=addresses['fastls-fetch']+'/fetch'
                request=urllib.request.Request('http://'+endpoint,data=json.dumps(payload).encode(),headers={'Content-Type':'application/json'})
                with direct.open(request,timeout=3) as response:
                    data=json.load(response)
                if kind=='jsonrpc': data=data['result']
                if not data.get('ok') or data.get('body')!='resource-ok': raise RuntimeError('request recovery failed: '+kind)
            load_start=time.monotonic()
            rounds=0
            with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
                while time.monotonic()-load_start<DURATION:
                    for future in [pool.submit(transaction,k) for k in ('fetch','jsonrpc','mitm')]: future.result()
                    # An interrupted slow request must not consume the next request slot.
                    request=urllib.request.Request('http://'+addresses['fastls-fetch']+'/fetch',data=json.dumps({'url':target+'/slow'}).encode(),headers={'Content-Type':'application/json'})
                    try:
                        with direct.open(request,timeout=.02) as response: response.read()
                    except (TimeoutError,OSError): pass
                    transaction('fetch')
                    rounds+=1
                    if not samples or time.monotonic()-load_start-samples[-1]['elapsed']>=5:
                        samples.append({'elapsed':time.monotonic()-load_start,'processes':{name:snapshot(p) for name,p in processes.items()}})
                    time.sleep(.4)
            for name in processes:
                warm=[s['processes'][name] for s in samples if 15<=s['elapsed']<=45]
                final=[s['processes'][name] for s in samples if s['elapsed']>=DURATION-60]
                if not warm or not final: raise RuntimeError('resource sample window missing')
                if max(s['rss'] for s in final)>max(s['rss'] for s in warm)+RSS_GROWTH: raise RuntimeError('RSS growth: '+name)
                if max(s['fds'] for s in final)>max(s['fds'] for s in warm)+FD_GROWTH: raise RuntimeError('descriptor growth: '+name)
            result.update(status='passed',completed_rounds=rounds,load_seconds=time.monotonic()-load_start)
    finally:
        for process in processes.values():
            if process.poll() is None: process.send_signal(signal.SIGTERM)
        for name,process in processes.items():
            try: process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                process.kill();process.wait();result['status']='failed';result['shutdown_timeout']=name
        for log in logs: log.close()
        server.shutdown();server.server_close()
        result['wall_seconds']=time.monotonic()-started
        (evidence/'resource-smoke.json').write_text(json.dumps(result,indent=2)+'\n',encoding='utf-8')
    if result['status']!='passed': raise SystemExit('resource or shutdown check failed')
    print(json.dumps({k:v for k,v in result.items() if k!='samples'},indent=2))


if __name__=='__main__':
    main()
