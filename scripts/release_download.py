"""Bounded release-byte download, with pinned identity and verified resumption."""
import hashlib
import http.client
import json
from pathlib import Path
import re
import time
import urllib.error
import urllib.request


def download(url, destination, size, sha256, *, attempts=3, timeout=20, opener=urllib.request.urlopen,
             sleep=time.sleep):
    if not isinstance(size,int) or size <= 0 or not re.fullmatch('[0-9a-f]{64}',sha256):
        raise ValueError('Pinned release size and SHA256 required')
    destination=Path(destination)
    destination.parent.mkdir(parents=True,exist_ok=True)
    partial=destination.with_name(destination.name+'.part')
    receipt=destination.with_name(destination.name+'.part.json')
    identity=dict(url=url,size=size,sha256=sha256)
    if destination.exists():
        if destination.stat().st_size == size and hashlib.sha256(destination.read_bytes()).hexdigest() == sha256:
            return dict(identity,verified=True,resumed=False,attempts=0)
        raise ValueError('Existing release bytes differ from pinned identity')
    saved=json.loads(receipt.read_text()) if receipt.exists() else {}
    offset=partial.stat().st_size if partial.exists() else 0
    if offset and (saved.get('identity') != identity or offset > size or
                   saved.get('prefix_sha256') != hashlib.sha256(partial.read_bytes()).hexdigest()):
        raise ValueError('Partial download identity or local prefix changed')
    if offset == size:
        if saved.get('prefix_sha256') != sha256:
            raise ValueError('Complete partial release SHA256 mismatch')
        partial.replace(destination)
        return dict(identity,verified=True,resumed=True,attempts=0)
    resumed=offset > 0
    for attempt in range(attempts):
        offset=partial.stat().st_size if partial.exists() else 0
        headers={'Accept-Encoding':'identity','User-Agent':'fastls-release-download'}
        if offset:
            headers['Range']='bytes='+str(offset)+'-'
            if saved.get('etag'): headers['If-Range']=saved['etag']
        try:
            with opener(urllib.request.Request(url,headers=headers),timeout=timeout) as response:
                status=response.status
                etag=response.headers.get('ETag')
                if response.headers.get('Content-Encoding','identity') != 'identity':
                    raise ValueError('Unexpected transfer encoding')
                if status == 206:
                    match=re.fullmatch(r'bytes (\d+)-(\d+)/(\d+)',response.headers.get('Content-Range',''))
                    if not match or tuple(map(int,match.groups())) != (offset,size-1,size):
                        raise ValueError('Invalid Content-Range')
                    if saved.get('etag') and etag != saved['etag']:
                        raise ValueError('Release identity changed during range request')
                elif status == 200:
                    offset=0  # Server ignored Range / If-Range; safely restart the whole body.
                else:
                    raise ValueError('Unexpected successful response status')
                if int(response.headers.get('Content-Length','-1')) != size-offset:
                    raise ValueError('Release response length differs from pinned identity')
                saved=dict(identity=identity,etag=etag)
                try:
                    with partial.open('ab' if offset else 'wb') as file:
                        while True:
                            chunk=response.read(64*1024)
                            if not chunk: break
                            if file.tell()+len(chunk)>size: raise ValueError('Oversized release body')
                            file.write(chunk)
                finally:
                    saved['prefix_sha256']=hashlib.sha256(partial.read_bytes()).hexdigest()
                    receipt.write_text(json.dumps(saved,indent=2)+'\n',encoding='utf-8')
                if partial.stat().st_size != size:
                    raise http.client.IncompleteRead(b'')
                if saved['prefix_sha256'] != sha256:
                    raise ValueError('Complete release SHA256 mismatch')
                partial.replace(destination)
                return dict(identity,verified=True,resumed=resumed or offset>0,attempts=attempt+1,etag=etag)
        except urllib.error.HTTPError as error:
            if error.code not in (404,408,429,500,502,503,504): raise
        except (urllib.error.URLError,TimeoutError,ConnectionError,http.client.IncompleteRead):
            pass
        if attempt+1<attempts: sleep(min(2**attempt,4))
    raise TimeoutError('Release download retry budget exhausted; partial bytes and identity retained')
