#!/usr/bin/env python3
"""Offline lock-format and pip tamper refusal checks. No third-party code is run."""
import hashlib
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def validate(text):
    entries = {}
    current = None
    hashes = []

    def finish():
        if current is not None:
            assert hashes, f'Missing hashes for {current}'
            assert current not in entries, f'Duplicate requirement {current}'
            entries[current] = tuple(hashes)

    continued = False
    for line in text.splitlines():
        if not line or line.startswith('#'):
            assert not continued, 'Unfinished continuation'
            continue
        if not line.startswith(' '):
            assert not continued, 'Requirement started before hash list ended'
            finish()
            match = re.fullmatch(r'([A-Za-z0-9_.-]+(?:\[etcd3\])?)==([A-Za-z0-9_.-]+) \\', line)
            assert match, f'Unpinned or unsupported requirement {line}'
            current = re.sub(r'[-_.]+', '-', match[1].split('[')[0]).lower()
            hashes = []
            continued = True
        else:
            assert current and continued, 'Orphan hash or missing continuation'
            match = re.fullmatch(r'    --hash=sha256:([a-f0-9]{64})( \\)?', line)
            assert match, 'Malformed hash'
            assert match[1] not in hashes, 'Duplicate hash'
            hashes.append(match[1])
            continued = bool(match[2])
    assert not continued, 'Trailing continuation'
    finish()
    assert entries, 'Empty lock'
    return entries


valid = 'fixture==1.0 \\\n    --hash=sha256:' + 'a' * 64 + '\n'
assert validate(valid) == {'fixture': ('a' * 64,)}
for invalid in ('', 'fixture>=1.0\n', 'fixture==1.0 \\\n',
                valid + 'other==1.0 \\\n', valid.replace('a' * 64, 'x' * 64),
                valid + valid, valid.replace('\n', '\n    --index-url=https://invalid/\n', 1)):
    try:
        validate(invalid)
    except AssertionError:
        pass
    else:
        raise AssertionError('Malformed fixture accepted')

for name in ('requirements.lock', 'build-requirements.lock'):
    entries = validate((ROOT / 'deploy/ha/patroni' / name).read_text())
    print(f'PASS: {name}: {len(entries)} individually hashed exact requirements', flush=True)

with tempfile.TemporaryDirectory(prefix='patroni-lock-offline-') as temp:
    root = Path(temp)
    wheel = root / 'hex_lock_fixture-1.0-py3-none-any.whl'
    with zipfile.ZipFile(wheel, 'w') as archive:
        archive.writestr('hex_lock_fixture/__init__.py', "VALUE = 'synthetic'\n")
        archive.writestr('hex_lock_fixture-1.0.dist-info/METADATA',
                         'Metadata-Version: 2.1\nName: hex-lock-fixture\nVersion: 1.0\n')
        archive.writestr('hex_lock_fixture-1.0.dist-info/WHEEL',
                         'Wheel-Version: 1.0\nGenerator: offline-fixture\nRoot-Is-Purelib: true\nTag: py3-none-any\n')
        archive.writestr('hex_lock_fixture-1.0.dist-info/RECORD', '')
    digest = hashlib.sha256(wheel.read_bytes()).hexdigest()
    lock = root / 'fixture.lock'
    lock.write_text(f'hex-lock-fixture==1.0 --hash=sha256:{digest}\n')

    def install(target):
        return subprocess.run([sys.executable, '-m', 'pip', 'install', '--disable-pip-version-check',
                               '--no-index', '--no-cache-dir', '--no-deps', '--ignore-installed',
                               '--only-binary=:all:', '--no-compile', '--require-hashes',
                               '--find-links', str(root), '--target', str(target), '-r', str(lock)],
                              text=True, capture_output=True, timeout=30)

    good = root / 'good'
    result = install(good)
    assert result.returncode == 0, result.stdout + result.stderr
    assert (good / 'hex_lock_fixture/__init__.py').is_file()
    # Change archive bytes without changing the expected hash, leaving a valid wheel.
    with zipfile.ZipFile(wheel, 'a') as archive:
        archive.writestr('tamper-marker.txt', 'changed synthetic bytes')
    bad = root / 'bad'
    result = install(bad)
    diagnostic = result.stdout + result.stderr
    assert result.returncode != 0 and 'DO NOT MATCH THE HASHES' in diagnostic, diagnostic
    assert not (bad / 'hex_lock_fixture').exists(), 'Hash-mismatched package installed'
    print('PASS: offline pip accepts intact synthetic wheel and rejects tampered bytes before installation', flush=True)
