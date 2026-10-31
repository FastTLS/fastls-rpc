"""Download, authenticate and inspect the exact release executables; no builds."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import zipfile
from release_download import download

MODULES = {
    'github.com/FastTLS/fastls':'v0.1.3-0.20261030152832-0dc4eea0f32e',
    'github.com/FastTLS/fastls/profile':'v0.1.1-0.20261030130219-e9e40f15c9ac',
    'github.com/FastTLS/ftransport':'v0.1.2-0.20261030131050-d9812320808e',
    'github.com/FastTLS/imitate':'v0.1.2-0.20261030131552-ebd18547d28a',
}


def run(*args):
    return subprocess.check_output(args,text=True,encoding='utf-8',stderr=subprocess.STDOUT)


def main():
    release_version, target = os.environ['RELEASE_VERSION'], os.environ['RELEASE_TARGET']
    version=release_version
    if not re.fullmatch(r'v\d+\.\d+\.\d+',version) or target not in {a+'-'+b for a in ('linux','windows','darwin') for b in ('amd64','arm64')}:
        raise SystemExit('invalid release version or target')
    out=Path(os.environ['RUNNER_TEMP'])/'service-release-evidence'
    out.mkdir(exist_ok=True)
    binaries=out/'bin'
    binaries.mkdir(exist_ok=True)
    proofs=[]
    for name,sha in [('fastls-fetch',os.environ['FETCH_SHA']),('fastls-rpc',os.environ['RPC_SHA']),('fastls-mitm',os.environ['MITM_SHA'])]:
        if not re.fullmatch('[0-9a-f]{40}',sha):
            raise SystemExit('full immutable source SHA required')
        version=(os.environ.get('MITM_RELEASE_VERSION','v0.1.2') if name=='fastls-mitm' else os.environ.get('RPC_RELEASE_VERSION',release_version) if name=='fastls-rpc' else release_version)
        if not re.fullmatch(r'v\d+\.\d+\.\d+',version):
            raise SystemExit('invalid component release version')
        directory=out/name
        directory.mkdir(exist_ok=True)
        repository='FastTLS/'+name
        ext='.zip' if target.startswith('windows-') else '.tar.gz'
        archive=name+'-'+version+'-'+target+ext
        release=json.loads(run('gh','api','repos/'+repository+'/releases/tags/'+version))
        asset=next(a for a in release['assets'] if a['name']==archive)
        if not re.fullmatch('sha256:[0-9a-f]{64}',asset.get('digest','')):
            raise SystemExit('release metadata lacks pinned SHA256')
        transfer=download(asset['browser_download_url'],directory/archive,asset['size'],asset['digest'][7:])
        (directory/'download.json').write_text(json.dumps(transfer,indent=2)+'\n',encoding='utf-8')
        run('gh','release','download',version,'--repo',repository,'--dir',str(directory),'--pattern','SHA256SUMS','--pattern','provenance.sigstore.json')
        digest=hashlib.sha256((directory/archive).read_bytes()).hexdigest()
        expected=dict((line.split()[1].lstrip('*'),line.split()[0]) for line in (directory/'SHA256SUMS').read_text().splitlines())
        if expected.get(archive)!=digest or asset.get('digest')!='sha256:'+digest:
            raise SystemExit('release archive checksum or registry digest mismatch')
        verification=run('gh','attestation','verify',str(directory/archive),'--repo',repository,'--bundle',str(directory/'provenance.sigstore.json'),'--signer-workflow',repository+'/.github/workflows/development-artifacts.yml','--source-digest',sha,'--source-ref','refs/tags/'+version,'--deny-self-hosted-runners','--format','json')
        (directory/'verified-attestation.json').write_text(verification,encoding='utf-8')
        statements=json.loads(verification)
        # Authenticate the archive's source commit as well as its workflow name.
        if not any(item['verificationResult']['statement']['predicate']['buildDefinition']['resolvedDependencies'][0]['digest'].get('gitCommit')==sha for item in statements):
            raise SystemExit('signed source commit mismatch')
        unpacked=directory/'unpacked'
        unpacked.mkdir(exist_ok=True)
        if ext=='.zip':
            with zipfile.ZipFile(directory/archive) as z:
                for member in z.infolist():
                    dest=(unpacked/member.filename).resolve()
                    if not dest.is_relative_to(unpacked.resolve()):
                        raise SystemExit('archive path escapes extraction directory')
                z.extractall(unpacked)
        else:
            with tarfile.open(directory/archive) as t:
                t.extractall(unpacked,filter='data')
        contents=unpacked/(name+'-'+version+'-'+target)
        info=dict(line.split('=',1) for line in (contents/'BUILD_INFO.txt').read_text().splitlines())
        if info.get('commit')!=sha or info.get('version')!=version or info.get('target')!=target.replace('-','/'):
            raise SystemExit('BUILD_INFO source/version/platform mismatch')
        components=['fastls-jsonrpc','fastls-grpc'] if name=='fastls-rpc' else [name]
        for component in components:
            executable=contents/(component+('.exe' if target.startswith('windows-') else ''))
            module_info=run('go','version','-m',str(executable))
            for module,pin in MODULES.items():
                if not re.search(r'\bdep\s+'+re.escape(module)+r'\s+'+re.escape(pin)+r'\s',module_info):
                    raise SystemExit('executable module mismatch: '+module)
            for setting in ['vcs.revision='+sha,'GOOS='+target.split('-')[0],'GOARCH='+target.split('-')[1]]:
                if setting not in module_info:
                    raise SystemExit('executable identity mismatch: '+setting)
            version_output=run(str(executable),'-version')
            if version not in version_output or sha not in version_output:
                raise SystemExit('runtime version identity mismatch')
            help_output=run(str(executable),'-help')
            if 'address-policy' not in help_output:
                raise SystemExit('release executable lacks policy flag')
            shutil.copy2(executable,binaries/executable.name)
            (directory/(component+'-identity.txt')).write_text(module_info+'\n'+version_output+'\n'+help_output,encoding='utf-8')
        proofs.append({'repository':repository,'source':sha,'version':version,'target':target,'archive':archive,'sha256':digest,'components':components})
    (out/'verified-releases.json').write_text(json.dumps(proofs,indent=2)+'\n',encoding='utf-8')
    with open(os.environ['GITHUB_ENV'],'a',encoding='utf-8') as file:
        file.write('FASTLS_POLICY_BIN_DIR='+binaries.resolve().as_posix()+'\n')
        file.write('RELEASE_EVIDENCE='+out.resolve().as_posix()+'\n')
    print(json.dumps(proofs,indent=2))


if __name__=='__main__':
    main()
