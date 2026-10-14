"""Prepare external test overlays; never run tests, mutate source, or contact cloud."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--verify-source', action='store_true')
    args = parser.parse_args()
    repo, output = args.repo.resolve(), args.output.resolve()
    package = Path(__file__).resolve().parent
    if output == repo or repo in output.parents:
        parser.error('output must be outside the repository')
    if output.exists() and any(output.iterdir()):
        parser.error('output must be a new or empty external directory')
    fixtures = ['integration_metadata_test.go', 'read_only_residual_test.go',
                'real_failure_test.go', 'actual_runtime_shutdown_test.go']
    manifest = json.loads((package/'source-manifest.json').read_text(encoding='utf-8'))
    for name in fixtures:
        data = (package/'_fixtures'/name).read_bytes()
        if hashlib.sha256(data).hexdigest() != manifest['fixtureSHA256'][name]:
            parser.error('fixture checksum mismatch: '+name)
    if args.verify_source:
        for item in manifest['productFiles']:
            path = repo/item['path']
            if not path.is_file():
                parser.error('missing source: '+item['path'])
            blob = subprocess.check_output(['git', 'hash-object', '--path', item['path'], str(path)], cwd=repo).decode().strip()
            if blob != item['gitBlob']:
                parser.error('source mismatch: '+item['path'])
    output.mkdir(parents=True, exist_ok=True)
    for name in fixtures:
        (output/name).write_bytes((package/'_fixtures'/name).read_bytes())
    provider = repo/'internal/connector/provider/openstack'
    common = {str(provider/'integration_test.go'): str(output/fixtures[0]),
              str(provider/'lbt147_additional_residual_test.go'): str(output/fixtures[1])}
    mappings = {
        'cloud-overlay.json': common,
        'real-failure-overlay.json': {**common, str(provider/'lbt147_additional_failure_test.go'): str(output/fixtures[2])},
        'actual-runtime-overlay.json': {str(repo/'internal/connector/app/additional_actual_runtime_test.go'): str(output/fixtures[3])},
    }
    for name, mapping in mappings.items():
        (output/name).write_text(json.dumps({'Replace': mapping}, indent=2)+'\n', encoding='utf-8')
    print('Prepared overlays only; tests/cloud calls=0')


if __name__ == '__main__':
    main()
