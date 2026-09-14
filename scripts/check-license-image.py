#!/usr/bin/env python3
"""Inspect a locally built release image; never publish or start the web server."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('image')
parser.add_argument('--arch', choices=('amd64', 'arm64'), required=True)
args = parser.parse_args()
root = Path(__file__).resolve().parent.parent
manifest = json.loads((root / 'internal/licensing/manifest.json').read_text())

def docker(*arguments):
    return subprocess.check_output(['docker', *arguments])

info = json.loads(docker('image', 'inspect', args.image))[0]
assert info['Architecture'] == args.arch, 'Wrong image architecture'
# Deliberately invalid operational configuration: this command must bypass it.
output = docker('run', '--rm', '--network=none', '--read-only', '--platform=linux/' + args.arch,
                '-e', 'MUNICHBRIEF_PAGE_SIZE=invalid', args.image, 'licenses').decode()
for notice in manifest['notices']:
    assert (root / notice['file']).read_text() in output, 'Embedded text missing: ' + notice['id']
container = docker('create', '--platform=linux/' + args.arch, args.image, 'licenses').decode().strip()
try:
    with tempfile.TemporaryDirectory(prefix='munichbrief-image-') as tmp:
        archive = Path(tmp) / 'image.tar'
        subprocess.run(['docker', 'export', '-o', str(archive), container], check=True)
        with tarfile.open(archive) as image:
            def read(path):
                stream = image.extractfile(path)
                assert stream is not None, 'Missing file: ' + path
                return stream.read()
            for file in ['LICENSE', 'THIRD_PARTY_NOTICES.md'] + [n['file'] for n in manifest['notices']] + [s['file'] for s in manifest['source_archives']]:
                assert read('usr/share/munichbrief/' + file) == (root / file).read_bytes(), 'Packaged file differs: ' + file
            packages = {c['id'].removeprefix('debian-'): c for c in manifest['components'] if c['kind'] == 'container'}
            actual = {p.name.split('/')[-1] for p in image.getmembers() if p.isfile() and p.name.startswith('var/lib/dpkg/status.d/') and not p.name.endswith('.md5sums')}
            assert actual == packages.keys(), 'Unreviewed base-image package: ' + repr(actual ^ packages.keys())
            for name, component in packages.items():
                status = read('var/lib/dpkg/status.d/' + name).decode()
                assert 'Version: ' + component['version'] + '\n' in status, 'Base package changed: ' + name
                assert read('usr/share/doc/' + name + '/copyright') == (root / ('LICENSES/debian-' + name + '.txt')).read_bytes(), 'Base copyright changed: ' + name
            for name in ['GPL-2', 'GPL-3', 'MPL-2.0']:
                assert read('usr/share/common-licenses/' + name), 'Base licence removed: ' + name
            # Compare the copied timezone data, not just a package label.
            zone = read('usr/share/zoneinfo/tzdata.zi')
            assert hashlib.sha256(zone).hexdigest() == manifest['container_zoneinfo_sha256'], 'Copied timezone data changed'
finally:
    docker('rm', '-v', container)
print('Verified binary notices, packaged texts/sources, base packages and timezone data for linux/' + args.arch)
