import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import tempfile
import threading
import unittest
from release_download import download
from evidence_archive import stage

BODY=b'verified original release bytes'*100
DIGEST=hashlib.sha256(BODY).hexdigest()


class Downloads(unittest.TestCase):
    def setUp(self):
        temp=tempfile.TemporaryDirectory();self.addCleanup(temp.cleanup);self.root=Path(temp.name)
        self.mode='resume';self.requests=[];owner=self
        class Handler(BaseHTTPRequestHandler):
            def log_message(self,*args):pass
            def do_GET(self):
                owner.requests.append(self.headers.get('Range'))
                if owner.mode in ('401','404','503'):
                    self.send_response(int(owner.mode));self.end_headers();return
                start=int(self.headers.get('Range','bytes=0-')[6:-1])
                ranged=start>0 and owner.mode!='restart'
                self.send_response(206 if ranged else 200)
                self.send_header('Content-Length',str(len(BODY)-start if ranged else len(BODY)))
                self.send_header('ETag','changed' if owner.mode=='etag' and ranged else 'fixed')
                if ranged:self.send_header('Content-Range',f'bytes {start+(1 if owner.mode=="range" else 0)}-{len(BODY)-1}/{len(BODY)}')
                self.end_headers()
                chunk=BODY[start:] if ranged else BODY
                if owner.mode in ('resume','restart','range','etag') and len(owner.requests)==1:
                    chunk=chunk[:64]
                if owner.mode=='digest':chunk=b'x'*len(BODY)
                self.wfile.write(chunk);self.wfile.flush();self.close_connection=True
        server=ThreadingHTTPServer(('127.0.0.1',0),Handler)
        thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
        self.addCleanup(server.server_close);self.addCleanup(server.shutdown)
        self.url=f'http://127.0.0.1:{server.server_port}/release'
        self.path=self.root/'release.tar.gz'

    def get(self,**kwargs):
        return download(self.url,self.path,len(BODY),DIGEST,sleep=lambda _:None,**kwargs)

    def test_truncated_body_resumes_and_verifies_complete_digest(self):
        result=self.get();self.assertTrue(result['resumed']);self.assertEqual(self.path.read_bytes(),BODY)
        self.assertEqual(self.requests,[None,'bytes=64-'])
        self.assertEqual(self.get()['attempts'],0)

    def test_ignored_range_restarts_safely(self):
        self.mode='restart';self.get();self.assertEqual(self.path.read_bytes(),BODY)

    def test_content_range_or_etag_substitution_fails(self):
        for mode in ('range','etag'):
            self.mode=mode;self.requests=[]
            for suffix in ('.part','.part.json'):
                self.path.with_name(self.path.name+suffix).unlink(missing_ok=True)
            with self.assertRaises(ValueError):self.get()
            self.assertFalse(self.path.exists())

    def test_digest_substitution_and_existing_wrong_bytes_fail(self):
        self.mode='digest'
        with self.assertRaises(ValueError):self.get()
        self.path.write_bytes(b'wrong')
        with self.assertRaises(ValueError):self.get()

    def test_permission_immediate_and_network_budget_bounded(self):
        self.mode='401'
        with self.assertRaises(Exception):self.get()
        self.assertEqual(len(self.requests),1)
        self.mode='503';self.requests=[]
        with self.assertRaises(TimeoutError):self.get(attempts=2)
        self.assertEqual(len(self.requests),2)

    def test_partial_identity_or_prefix_tampering_fails(self):
        with self.assertRaises(TimeoutError):self.get(attempts=1)
        p=self.path.with_name(self.path.name+'.part');p.write_bytes(b'tampered')
        with self.assertRaises(ValueError):self.get()


class Evidence(unittest.TestCase):
    def test_selects_proof_and_failure_logs_without_archives_or_executables(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);src=root/'source';src.mkdir()
            for name in ['verified-releases.json','installed-policy.log','fastls-rpc/provenance.sigstore.json',
                         'fastls-rpc/fastls-grpc-identity.txt','fastls-rpc/SHA256SUMS','bin/leak.json',
                         'fastls-rpc/unpacked/test.log','release.zip','release.tar.gz','fastls.exe']:
                p=src/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text('record')
            result=stage(src,root/'attempt1',123,1)
            self.assertEqual(len(result['files']),5)
            self.assertNotIn('bin/leak.json',result['files'])
            self.assertEqual(result['attempt'],'1')
            with self.assertRaises(ValueError):stage(src,root/'attempt1',123,2)
            stage(src,root/'attempt2',123,2)


if __name__=='__main__':unittest.main()
