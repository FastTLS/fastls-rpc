"""Select development feedback or a frozen candidate without weakening main gates."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import hashlib
import re
import urllib.request

CURRENT = '1.26.10'
NEXT = '1.27.3'


def native_run_valid(run, jobs):
    matrix = {f'{platform} / Go {version}' for platform in ('ubuntu-24.04', 'windows-2022', 'macos-15') for version in (CURRENT, NEXT)}
    names = {j['name'] for j in jobs if j.get('conclusion') == 'success'}
    return (run.get('conclusion') == 'success' and run.get('event') == 'push' and run.get('head_branch') == 'main'
            and matrix <= names and 'Dependency policy' in names and 'Accepted validation plan' in names
            and any(name.startswith('Race, coverage and CLI smoke') for name in names)
            and all(j.get('conclusion') == 'success' for j in jobs))


def native_recipe(text):
    # A prior success is reusable only for unchanged native commands, actions,
    # environments, matrix expansion and dependencies. Ignore just the job-level
    # selection condition, whose new coordination branch is unit tested.
    selected = []
    for name, end in (('dependency-policy', 'test'), ('test', 'quality'), ('quality', 'accepted')):
        part = text.split('  '+name+':\n', 1)[1].split('  '+end+':\n', 1)[0]
        selected.append('\n'.join(line for line in part.splitlines() if not line.startswith('    if:')))
    selected.append(text.split('\nenv:\n', 1)[1].split('\njobs:', 1)[0])
    return hashlib.sha256('\n'.join(selected).encode()).hexdigest()


def native_baseline():
    repo, token = os.environ.get('GITHUB_REPOSITORY'), os.environ.get('GH_TOKEN')
    if not repo or not token:
        return None
    def api(path):
        request = urllib.request.Request('https://api.github.com/repos/'+repo+path,
            headers={'Authorization': 'Bearer '+token, 'Accept': 'application/vnd.github+json', 'User-Agent': 'native-input-reuse'})
        with urllib.request.urlopen(request, timeout=15) as response:
            return json.load(response)
    try:
        current = native_recipe(Path('.github/workflows/ci.yml').read_text())
        runs = api('/actions/workflows/ci.yml/runs?status=success&event=push&branch=main&per_page=20')['workflow_runs']
        for run in runs:
            sha = run['head_sha']
            if not re.fullmatch('[0-9a-f]{40}', sha):
                continue
            if subprocess.run(['git', 'merge-base', '--is-ancestor', sha, 'HEAD'], capture_output=True).returncode:
                continue
            changed = subprocess.check_output(['git', 'diff', '--name-only', sha, 'HEAD'], text=True).splitlines()
            if choose('push', 'refs/heads/main', 'auto', changed)['mode'] != 'coordination':
                continue
            previous = subprocess.check_output(['git', 'show', sha+':.github/workflows/ci.yml'], text=True)
            if native_recipe(previous) != current:
                continue
            jobs = api('/actions/runs/'+str(run['id'])+'/jobs?per_page=100')['jobs']
            if len(jobs) < 100 and native_run_valid(run, jobs):
                return dict(run_id=run['id'], sha=sha, recipe_sha256=current, verified=True)
    except (OSError, ValueError, KeyError, IndexError, subprocess.CalledProcessError):
        return None
    return None


def choose(event, ref, requested, paths):
    if requested not in ('auto', 'quick', 'candidate'):
        raise ValueError('unknown validation mode')
    tag = ref.startswith('refs/tags/')
    docs = paths is not None and bool(paths) and all(
        p.endswith(('.md', '.rst', '.txt')) or p in ('LICENSE', 'NOTICE') for p in paths)
    coordination_files = {
        'scripts/ci_plan.py', 'scripts/test_ci_plan.py',
        '.github/workflows/ci.yml', '.github/workflows/workspace-integration.yml',
        '.github/workflows/address-policy-integration.yml',
        'integration/policy/policy_e2e_test.go', 'integration/policy/mitm_gateway_e2e_test.go',
    }
    coordination = paths is not None and bool(paths) and all(p in coordination_files for p in paths)
    if coordination:
        root = Path(__file__).resolve().parents[1]
        coordination = all(not p.endswith('.go') or (root/p).is_file() and
                           (root/p).read_text(encoding='utf-8').startswith('//go:build policy_e2e\n') for p in paths)
    if coordination and not tag and requested != 'candidate':
        # These tagged contracts run in Workspace integration. Normal Go
        # package inputs and dependencies are unchanged; preserve its exact
        # earlier native evidence instead of rebuilding the same matrix.
        mode = 'coordination'
    elif docs and not tag and requested != 'candidate':
        mode = 'docs'
    elif tag or ref in ('refs/heads/main', 'refs/heads/v2'):
        mode = 'candidate'
    elif requested == 'candidate' or paths is None or not paths:
        mode = 'candidate'
    elif any(not (p.endswith('.go') or p in ('go.mod', 'go.sum') or p.startswith(('scripts/', '.github/'))) for p in paths):
        mode = 'candidate'
    else:
        mode = 'quick'
    matrix = [] if mode in ('docs', 'coordination') else [dict(os=platform, go=version)
        for platform in (('ubuntu-24.04', 'windows-2022', 'macos-15') if mode == 'candidate'
                         else ('ubuntu-24.04', 'windows-2022'))
        for version in ((CURRENT, NEXT) if mode == 'candidate' else (CURRENT,))]
    return dict(mode=mode, matrix=dict(include=matrix), quality=mode == 'candidate')


def gate(plan, jobs):
    if plan.get('mode') not in ('docs', 'coordination', 'quick', 'candidate') or plan.get('quality') != (plan['mode'] == 'candidate'):
        raise ValueError('invalid validation plan')
    if plan['mode'] == 'coordination':
        proof = plan.get('native_baseline', {})
        if proof.get('verified') is not True or not re.fullmatch('[0-9a-f]{40}', proof.get('sha', '')) or not isinstance(proof.get('run_id'), int):
            raise ValueError('coordination skips require verified native evidence')
    expected = {'plan': True, 'dependency-policy': plan['mode'] in ('quick', 'candidate'),
                'test': plan['mode'] in ('quick', 'candidate'), 'quality': plan['quality']}
    for name, required in expected.items():
        actual = jobs.get(name, {}).get('result')
        if actual != ('success' if required else 'skipped'):
            raise ValueError(f'{name}: expected {"success" if required else "skipped"}, received {actual}')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--gate', action='store_true')
    args = parser.parse_args()
    if args.gate:
        gate(json.loads(os.environ['VALIDATION_PLAN']), json.loads(os.environ['VALIDATION_JOBS']))
        return
    event = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text())
    base = event.get('pull_request', {}).get('base', {}).get('sha') or event.get('before')
    head = event.get('pull_request', {}).get('head', {}).get('sha') or os.environ.get('GITHUB_SHA')
    if not base or set(base) == {'0'}:
        base = 'HEAD^'
    try:
        paths = subprocess.check_output(['git', 'diff', '--name-only', base, head or 'HEAD'], text=True).splitlines()
    except subprocess.CalledProcessError:
        paths = None
    plan = choose(os.environ['GITHUB_EVENT_NAME'], os.environ['GITHUB_REF'],
                  event.get('inputs', {}).get('validation', 'auto'), paths)
    if plan['mode'] == 'coordination':
        proof = native_baseline()
        if proof:
            plan['native_baseline'] = proof
        else:
            plan = choose(os.environ['GITHUB_EVENT_NAME'], os.environ['GITHUB_REF'], 'candidate', paths)
            print('No matching successful native baseline; candidate validation is required')
    with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
        output.write('plan=' + json.dumps(plan, separators=(',', ':')) + '\n')
        output.write('mode=' + plan['mode'] + '\n')
        output.write('matrix=' + json.dumps(plan['matrix'], separators=(',', ':')) + '\n')
        output.write('quality=' + str(plan['quality']).lower() + '\n')
    print(json.dumps(plan))


if __name__ == '__main__':
    main()
