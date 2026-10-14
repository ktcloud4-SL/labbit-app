"""Reproduce pinned follow-up source; cloud mutations require --cloud."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import zipfile

CLOUD_FLAGS = ['LABBIT_OPENSTACK_INTEGRATION','LABBIT_OPENSTACK_MUTATION_TEST','LABBIT_OPENSTACK_M2_TEST','LABBIT_OPENSTACK_M3_TEST','LABBIT_OPENSTACK_INTERNET_POLICY_TEST','LABBIT_OPENSTACK_M2_WSS_TEST','LABBIT_OPENSTACK_M3_WSS_TEST','LABBIT_OPENSTACK_VM_ACCESS_TEST','LABBIT_OPENSTACK_APP_AUDIT','LABBIT_OPENSTACK_RESIDUAL_AUDIT']

def reproduction_environment(repo, cloud=False, inherited=None):
    env = dict(os.environ if inherited is None else inherited)
    env.update({'LABBIT_EVIDENCE_REPO':str(repo), 'PYTHONIOENCODING':'utf-8'})
    for name in CLOUD_FLAGS:
        env[name] = '0'
    # Only the selected cloud runner explicitly enables its own fixture flags.
    return env

def git(repo, *args):
    return subprocess.check_output(['git', *args], cwd=repo, stderr=subprocess.PIPE).decode('utf-8').strip()

def verify_source(repo, manifest):
    prefix = 'docs/connector/evidence/LBT-147/2026-10-12-review-followup/'
    expected = set(manifest['localChangedFiles'])
    changed = set(git(repo, 'diff', '--name-only', manifest['head']).splitlines())
    changed.update(git(repo, 'diff', '--cached', '--name-only', manifest['head']).splitlines())
    changed.update(git(repo, 'ls-files', '--others', '--exclude-standard').splitlines())
    evidence_go = sorted(p for p in changed if p.startswith(prefix)
                         and (p.endswith('.go') or Path(p).name in {'go.mod', 'go.sum'})
                         and not p.startswith(prefix+'_fixtures/'))
    if evidence_go:
        raise ValueError('Unaudited Evidence Go source could change execution: '+', '.join(evidence_go))
    unexpected = sorted(p for p in changed if p not in expected and not p.startswith(prefix))
    if unexpected:
        raise ValueError('Unexpected source outside audited scope: '+', '.join(unexpected))
    ignored = [p for p in git(repo, 'ls-files', '--others', '--ignored', '--exclude-standard', '--', '*.go', 'go.mod', 'go.sum').splitlines() if not p.startswith('web/node_modules/.pnpm/')]
    if ignored:
        raise ValueError('Ignored Go source could change execution: '+', '.join(ignored))
    for item in manifest['files']:
        path = repo/item['path']
        if not path.is_file() or git(repo, 'hash-object', '--path', item['path'], item['path']) != item['gitBlob']:
            raise ValueError('Audited Git blob mismatch: '+item['path'])

def verify_artifacts(evidence, checksums):
    expected = {item['file'] for item in checksums['artifacts']}
    actual = {p.relative_to(evidence).as_posix() for p in evidence.rglob('*')
              if p.is_file() and p != evidence/'artifact-checksums.json'
              and '__pycache__' not in p.relative_to(evidence).parts and p.suffix != '.pyc'}
    if actual != expected:
        raise ValueError('Evidence artifact set mismatch; extra: '+', '.join(sorted(actual-expected))
                         +'; missing: '+', '.join(sorted(expected-actual)))
    for item in checksums['artifacts']:
        if hashlib.sha256((evidence/item['file']).read_bytes()).hexdigest() != item['sha256']:
            raise ValueError('Evidence artifact mismatch: '+item['file'])

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', type=Path, required=True)
    parser.add_argument('--output', type=Path)
    parser.add_argument('--verify-only', action='store_true')
    parser.add_argument('--cloud', action='store_true')
    parser.add_argument('--cloud-config', type=Path)
    parser.add_argument('--private-key', type=Path)
    parser.add_argument('--cloud-name', default='labbit-test')
    args = parser.parse_args()
    repo = args.repo.resolve(strict=True)
    evidence = Path(__file__).resolve().parent
    manifest = json.loads((evidence/'source-manifest.json').read_text(encoding='utf-8'))
    checksums = json.loads((evidence/'artifact-checksums.json').read_text(encoding='utf-8'))
    verify_artifacts(evidence, checksums)
    verify_source(repo, manifest)
    if args.verify_only:
        print('Pinned source and artifact verification PASS')
        return
    if args.output is None:
        raise ValueError('--output must name a new external directory')
    output = args.output.resolve()
    if output.exists() or output == repo or repo in output.parents:
        raise ValueError('Output must be a new directory outside the repository')
    if args.cloud:
        if not args.cloud_config or not args.private_key:
            raise ValueError('--cloud requires --cloud-config and --private-key')
        args.cloud_config = args.cloud_config.resolve(strict=True)
        args.private_key = args.private_key.resolve(strict=True)
    output.mkdir(parents=True)
    extracted = output/'archive'
    with zipfile.ZipFile(evidence/'audit-logs.zip') as archive:
        expected = json.loads((evidence/'archive-manifest.json').read_text(encoding='utf-8'))['entries']
        for item in expected:
            data = archive.read(item['path'])
            if hashlib.sha256(data).hexdigest() != item['sha256'] or len(data) != item['bytes']:
                raise ValueError('Archive entry mismatch: '+item['path'])
            target = (extracted/item['path']).resolve()
            if extracted.resolve() not in target.parents:
                raise ValueError('Unsafe archive path')
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
    runner = extracted/'portable_run.py'
    env = reproduction_environment(repo, args.cloud)
    if args.cloud:
        env.update({'LABBIT_PROVIDER_CONFIG_FILE':str(args.cloud_config), 'LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE':str(args.private_key), 'OS_CLOUD':args.cloud_name})
        modes = ['cloud']
    else:
        # Dependencies must already be available in the chosen Python and PATH.
        modes = ['auto','web','contracts']
    for mode in modes:
        subprocess.run([sys.executable, str(runner), mode], cwd=repo, env=env, check=True)
    verify_source(repo, manifest)
    print('Reproduction PASS; outputs: '+str(extracted))

if __name__ == '__main__':
    main()
