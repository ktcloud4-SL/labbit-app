"""Operator-only archive of PUBLIC signed bytes; never authorizes/executes control.

Preview is default. Existing INTENT/UNKNOWN is HOLD, never a repeat PUT. A STORED
receipt can only repeat fixed-version readback. The journal directory must be
local durable storage writable only by the trusted operator (Windows: owner DACL).
Keep its receipt; losing/replacing it does not authorize retry of an uncertain PUT.
"""
import argparse
import base64
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

MAX = 256 * 1024
UTC = dt.timezone.utc


class Hold(Exception):
    pass


def strict(raw):
    def pairs(items):
        out = {}
        for key, value in items:
            if key in out:
                raise Hold('DUPLICATE_JSON_KEY')
            out[key] = value
        return out
    try:
        return json.loads(raw, object_pairs_hook=pairs, parse_constant=lambda _: (_ for _ in ()).throw(Hold('NONFINITE_JSON')))
    except (ValueError, UnicodeError, RecursionError) as e:
        raise Hold('JSON_NOT_VERIFIED') from e


def stamp(value):
    if not isinstance(value, str) or not value.endswith('Z'):
        raise Hold('UTC_REQUIRED')
    try:
        return dt.datetime.fromisoformat(value[:-1] + '+00:00')
    except ValueError as e:
        raise Hold('UTC_REQUIRED') from e


def read_public(path):
    p = Path(path)
    if not p.is_absolute() or p.is_symlink() or not p.is_file():
        raise Hold('SOURCE_NOT_VERIFIED')
    with p.open('rb') as f:
        raw = f.read(MAX + 1)
    if not 0 < len(raw) <= MAX:
        raise Hold('SIZE_NOT_VERIFIED')
    envelope = strict(raw)
    if not isinstance(envelope, dict) or set(envelope) != {'Version', 'KeyID', 'Payload', 'Signature'} or envelope['Version'] != 'labbit-dr-control/v1':
        raise Hold('PUBLIC_ENVELOPE_REQUIRED')
    try:
        if len(base64.b64decode(envelope['Signature'], validate=True)) != 64:
            raise Hold('SIGNATURE_SHAPE_NOT_VERIFIED')
    except (ValueError, TypeError) as e:
        raise Hold('SIGNATURE_SHAPE_NOT_VERIFIED') from e
    payload = envelope['Payload']
    approval = {'Version', 'Action', 'RequestSHA256', 'Principal', 'ID', 'NotBefore', 'ExpiresAt'}
    observation = {'Version', 'Source', 'Binding', 'ObservedAt', 'ValidUntil', 'AdmissionBlocked', 'SingleWriter', 'Fencing', 'WorkInventory', 'PodCoverage', 'WorkReconciled'}
    if not isinstance(payload, dict) or set(payload) not in (approval, observation):
        raise Hold('PUBLIC_TYPED_DOCUMENT_REQUIRED')
    # Shape only: the control consumer must verify authorized keys/signatures,
    # purpose, exact request and live TTL. Archive success is never approval.
    return raw


def validate(plan, raw):
    keys = {'version', 'account', 'actor', 'profile', 'aws', 'region', 'bucket', 'prefix', 'key', 'sha256', 'size', 'not_before', 'valid_until', 'retain_until', 'owner', 'enable_upload'}
    if not isinstance(plan, dict) or set(plan) != keys or plan['version'] != 'dr-control-artifact-store/v1':
        raise Hold('PLAN_NOT_VERIFIED')
    if not re.fullmatch(r'[0-9]{12}', plan['account']) or not plan['actor'].startswith('arn:aws:iam::'+plan['account']+':user/'):
        raise Hold('ACTOR_NOT_VERIFIED')
    if plan['region'] not in ('ap-northeast-2', 'ap-northeast-3') or not Path(plan['aws']).is_absolute() or not plan['profile'] or not plan['owner']:
        raise Hold('ENDPOINT_NOT_VERIFIED')
    if not re.fullmatch(r'[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]', plan['bucket']):
        raise Hold('BUCKET_NOT_VERIFIED')
    if not plan['prefix'] or plan['prefix'].endswith('/') or not plan['key'].startswith(plan['prefix']+'/') or any(x in plan['key'] for x in ('\n', '\r', '\0', '../', '//')):
        raise Hold('PREFIX_NOT_VERIFIED')
    if type(plan['size']) is not int or plan['size'] != len(raw) or plan['sha256'] != hashlib.sha256(raw).hexdigest():
        raise Hold('SOURCE_HASH_NOT_VERIFIED')
    start, end, retain = map(stamp, (plan['not_before'], plan['valid_until'], plan['retain_until']))
    if end <= start or end-start > dt.timedelta(hours=1) or retain < end+dt.timedelta(days=30) or type(plan['enable_upload']) is not bool:
        raise Hold('WINDOW_OR_RETENTION_NOT_VERIFIED')


def write_receipt(path, record, initial=False):
    if initial:
        with path.open('x', encoding='utf-8', newline='\n') as f:
            json.dump(record, f, sort_keys=True); f.write('\n'); f.flush(); os.fsync(f.fileno())
    else:
        # Keep the old durable INTENT/receipt until the new complete file exists.
        temp = path.with_name(path.name+'.next')
        with temp.open('x', encoding='utf-8', newline='\n') as f:
            json.dump(record, f, sort_keys=True); f.write('\n'); f.flush(); os.fsync(f.fileno())
        os.replace(temp, path)


def command(plan, service, action, args):
    region = plan['region']
    endpoint = 's3' if service == 's3api' else service
    env = {k: v for k, v in os.environ.items() if not k.upper().startswith('AWS_')}
    env.update(AWS_PAGER='', AWS_CLI_AUTO_PROMPT='off', AWS_MAX_ATTEMPTS='1', AWS_IGNORE_CONFIGURED_ENDPOINT_URLS='true')
    argv = [plan['aws'], service, action, *args, '--profile', plan['profile'], '--region', region, '--endpoint-url', f'https://{endpoint}.{region}.amazonaws.com', '--output', 'json', '--no-cli-pager', '--cli-connect-timeout', '3', '--cli-read-timeout', '5']
    try:
        p = subprocess.run(argv, env=env, capture_output=True, timeout=12)
    except (OSError, subprocess.TimeoutExpired) as e:
        raise Hold('AWS_RESPONSE_NOT_VERIFIED') from e
    if p.returncode or len(p.stdout) > MAX:
        raise Hold('AWS_RESPONSE_NOT_VERIFIED')
    return strict(p.stdout)


def store(plan, source, receipt, execute=False, run=command, now=lambda: dt.datetime.now(UTC)):
    raw = read_public(source)
    validate(plan, raw)
    if not execute:
        return {'status': 'PREVIEW_ONLY', 'execution_gate': 'HOLD'}
    if not plan['enable_upload'] or not stamp(plan['not_before']) <= now() < stamp(plan['valid_until']):
        raise Hold('UPLOAD_DISABLED_OR_OUTSIDE_WINDOW')
    receipt = Path(receipt)
    if not receipt.is_absolute() or receipt.is_symlink() or not receipt.parent.is_dir():
        raise Hold('RECEIPT_PATH_NOT_VERIFIED')
    # O_EXCL lock deliberately survives a process crash; operator investigates.
    lock = receipt.with_name(receipt.name+'.lock')
    try:
        fd = os.open(lock, os.O_CREAT|os.O_EXCL|os.O_WRONLY, 0o600)
    except FileExistsError as e:
        raise Hold('CONCURRENT_OR_UNRESOLVED_STORE') from e
    os.close(fd)
    try:
        ident = run(plan, 'sts', 'get-caller-identity', [])
        if ident.get('Account') != plan['account'] or ident.get('Arn') != plan['actor']:
            raise Hold('IDENTITY_NOT_VERIFIED')
        binding = hashlib.sha256(json.dumps(plan, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
        if receipt.exists():
            r = strict(receipt.read_bytes())
            if r.get('plan_sha256') != binding or r.get('status') not in ('STORED', 'READBACK_VERIFIED'):
                raise Hold('EXISTING_INTENT_UNKNOWN_OR_CONFLICT')
        else:
            common = ['--bucket', plan['bucket'], '--expected-bucket-owner', plan['account']]
            if run(plan, 's3api', 'get-bucket-versioning', common).get('Status') != 'Enabled':
                raise Hold('VERSIONING_NOT_VERIFIED')
            if not stamp(plan['not_before']) <= now() < stamp(plan['valid_until']):
                raise Hold('WINDOW_EXPIRED_BEFORE_INTENT')
            r = {'plan_sha256': binding, 'status': 'INTENT', 'at': now().isoformat(), 'execution_gate': 'HOLD'}
            write_receipt(receipt, r, initial=True)
            with tempfile.TemporaryDirectory(prefix='dr-control-public-', dir=receipt.parent) as d:
                body = Path(d)/'signed.json'
                with body.open('xb') as f:
                    f.write(raw); f.flush(); os.fsync(f.fileno())
                if not stamp(plan['not_before']) <= now() < stamp(plan['valid_until']):
                    # An INTENT exists, but no request has been sent. Preserve it
                    # for investigation; this command never silently retries it.
                    r['not_sent_reason'] = 'WINDOW_EXPIRED_BEFORE_PUT'
                    write_receipt(receipt, r)
                    raise Hold('WINDOW_EXPIRED_BEFORE_PUT')
                try:
                    ack = run(plan, 's3api', 'put-object', [*common, '--key', plan['key'], '--body', str(body), '--if-none-match', '*', '--server-side-encryption', 'AES256', '--checksum-sha256', base64.b64encode(hashlib.sha256(raw).digest()).decode(), '--content-type', 'application/json'])
                except Exception as e:
                    r['status'] = 'UNKNOWN'; write_receipt(receipt, r)
                    raise Hold('PUT_UNKNOWN_NO_AUTOMATIC_RETRY') from e
            version = ack.get('VersionId')
            if not isinstance(version, str) or version in ('', 'null') or ack.get('ServerSideEncryption') != 'AES256':
                r['status'] = 'UNKNOWN'; write_receipt(receipt, r)
                raise Hold('PUT_METADATA_UNKNOWN')
            r.update(status='STORED', reference={'Bucket': plan['bucket'], 'Key': plan['key'], 'VersionID': version, 'SHA256': plan['sha256'], 'Size': plan['size']}, encryption='AES256', retain_until=plan['retain_until'], owner=plan['owner'])
            write_receipt(receipt, r)
        ref = r['reference']
        if ref.get('Bucket') != plan['bucket'] or ref.get('Key') != plan['key'] or ref.get('SHA256') != plan['sha256'] or ref.get('Size') != plan['size'] or not isinstance(ref.get('VersionID'), str) or ref['VersionID'] in ('','null'):
            raise Hold('RECEIPT_REFERENCE_NOT_VERIFIED')
        fixed = ['--bucket', ref['Bucket'], '--key', ref['Key'], '--version-id', ref['VersionID'], '--expected-bucket-owner', plan['account']]
        head = run(plan, 's3api', 'head-object', fixed)
        if head.get('VersionId') != ref['VersionID'] or head.get('ContentLength') != plan['size'] or head.get('ServerSideEncryption') != 'AES256' or head.get('DeleteMarker'):
            raise Hold('FULL_OBJECT_METADATA_NOT_VERIFIED')
        with tempfile.TemporaryDirectory(prefix='dr-control-readback-', dir=receipt.parent) as d:
            target = Path(d)/'original.json'
            meta = run(plan, 's3api', 'get-object', [*fixed, '--range', 'bytes=0-'+str(plan['size']-1), str(target)])
            with target.open('rb') as f:
                got = f.read(MAX+1)
            if meta.get('VersionId') != ref['VersionID'] or meta.get('ContentLength') != plan['size'] or meta.get('ServerSideEncryption') != 'AES256' or meta.get('DeleteMarker') or got != raw:
                raise Hold('FIXED_VERSION_READBACK_NOT_VERIFIED')
        r['status'] = 'READBACK_VERIFIED'; r['verified_at'] = now().isoformat()
        write_receipt(receipt, r)
        return r
    finally:
        lock.unlink()


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--plan', required=True); p.add_argument('--source', required=True); p.add_argument('--receipt', required=True)
    p.add_argument('--execute', action='store_true')
    a = p.parse_args()
    try:
        result = store(strict(Path(a.plan).read_bytes()), a.source, a.receipt, a.execute)
    except Exception:
        print(json.dumps({'status':'NOT_VERIFIED', 'reason':'STORE_HOLD_INSPECT_RECEIPT_NO_REPEAT_PUT', 'execution_gate':'HOLD'}))
        return 2
    print(json.dumps(result)); return 0


if __name__ == '__main__':
    raise SystemExit(main())
