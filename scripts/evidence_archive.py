"""Stage small verification records; official bytes remain in Releases."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import sys


def stage(source, target, run_id, attempt):
    source,target=Path(source),Path(target)
    if target.exists():
        raise ValueError('Evidence destination must be unique for this attempt')
    target.mkdir(parents=True)
    files={}
    if source.exists():
        for path in sorted(source.rglob('*')):
            relative=path.relative_to(source)
            if any(p in ('bin','unpacked') for p in relative.parts) or path.is_symlink() or not path.is_file():
                continue
            if not (path.suffix in ('.json','.log') or path.name.endswith('-identity.txt') or path.name=='SHA256SUMS'):
                continue
            if path.stat().st_size > 8*1024*1024:
                raise ValueError('Unexpected oversized evidence: '+str(relative))
            copy=target/relative
            copy.parent.mkdir(parents=True,exist_ok=True)
            shutil.copyfile(path,copy)
            files[relative.as_posix()]=dict(size=copy.stat().st_size,sha256=hashlib.sha256(copy.read_bytes()).hexdigest())
    manifest=dict(schema=1,run_id=str(run_id),attempt=str(attempt),files=files,
                  archive_scope='Only selected evidence files; no claim of recomputing a workflow ZIP digest.',
                  official_bytes='Original signed release archives remain in GitHub Releases.')
    (target/'evidence-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n',encoding='utf-8')
    return manifest


if __name__=='__main__':
    stage(*sys.argv[1:3],os.environ['GITHUB_RUN_ID'],os.environ['GITHUB_RUN_ATTEMPT'])
