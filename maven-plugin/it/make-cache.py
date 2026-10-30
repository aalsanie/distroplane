#!/usr/bin/env python3
"""Create a controlled, fake-but-hash-verified native cache for Maven integration tests.
Only for CI fixtures; never for public installation or publication.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import sys

def main():
    p=argparse.ArgumentParser()
    p.add_argument('--repo',required=True)
    p.add_argument('--binary',required=True)
    p.add_argument('--platform',choices=['linux','darwin','windows'],required=True)
    p.add_argument('--arch',choices=['amd64','arm64'],required=True)
    a=p.parse_args()
    raw=Path(a.binary).read_bytes()
    # This fixture is deliberately not a GitHub release checksum; it must only
    # be used with the locally-generated offline test cache.
    fixture_digest=hashlib.sha256(b'distroplane-maven-fixture-v1\0'+raw).hexdigest()
    root=Path(a.repo).resolve()/'distroplane'/'cli'/'0.9.2'/f'{a.platform}-{a.arch}'/f'sha256-{fixture_digest}'
    if root.exists():
        raise SystemExit('refusing to overwrite existing fixture cache')
    root.mkdir(parents=True)
    extension='.exe' if a.platform=='windows' else ''
    entries=[]
    for name in ['distroplane','distroplane-provider-homebrew','distroplane-provider-npm','distroplane-provider-sdkman','distroplane-provider-winget']:
        rel=f"{'bin' if name=='distroplane' else 'libexec'}/{name}{extension}"
        f=root/rel
        f.parent.mkdir(parents=True,exist_ok=True)
        shutil.copyfile(a.binary,f)
        if a.platform!='windows':f.chmod(0o700)
        entries.append(dict(path=rel,sha256=hashlib.sha256(raw).hexdigest(),size=len(raw),mode='0755'))
    (root/'PACKAGE-METADATA.json').write_text(json.dumps(dict(schemaVersion='1',version='0.9.2',os=a.platform,arch=a.arch,cgoEnabled=False,files=entries)),encoding='utf-8')
    (root/'archive.sha256').write_text(fixture_digest,encoding='utf-8')
    print(fixture_digest)
if __name__=='__main__':main()
