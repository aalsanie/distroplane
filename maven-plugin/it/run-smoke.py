#!/usr/bin/env python3
"""Native Maven integration smoke: explicit goals, offline cache, recovery and reactor."""
import argparse
import json
from pathlib import Path
import shutil
import subprocess
import sys

def run(command, ok=True, cwd=None):
    print('testing:', ' '.join(command), flush=True)
    p=subprocess.run(command, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=240, cwd=cwd)
    if (p.returncode == 0) != ok:
        print(p.stdout[-8000:])
        raise AssertionError(f'expected success={ok}, return code {p.returncode}')
    return p.stdout

def main():
    ap=argparse.ArgumentParser()
    ap.add_argument('--maven',default='mvn')
    ap.add_argument('--repo',required=True)
    ap.add_argument('--fixture',required=True)
    ap.add_argument('--source',required=True)
    args=ap.parse_args()
    if sys.platform == 'win32' and args.maven == 'mvn':
        args.maven = 'mvn.cmd'
    root=Path(args.source).resolve()
    repo=Path(args.repo).resolve()
    fixture=Path(args.fixture).resolve()
    simple=root/'maven-plugin/it/simple'
    reactor=root/'maven-plugin/it/reactor'
    cmd=[args.maven,'-B','-ntp','-o',f'-Dmaven.repo.local={repo}']
    goal='io.github.aalsanie:distroplane-maven-plugin:1.0.0-SNAPSHOT:'
    import functools
    invoke=functools.partial(run, cwd=simple)
    (simple/'.fixture-pending').unlink(missing_ok=True)
    (simple/'.fixture-hang').unlink(missing_ok=True)
    shutil.rmtree(simple/'.distroplane',ignore_errors=True)
    invoke(cmd+['-f',str(simple/'pom.xml'),goal+'validate'])
    invoke(cmd+['-f',str(simple/'pom.xml'),goal+'plan'])
    invoke(cmd+['-f',str(simple/'pom.xml'),goal+'publish'],ok=False) # no invisible approval
    if (simple/'.distroplane').exists():raise AssertionError('unapproved publication created durable state')
    (simple/'.fixture-pending').touch()
    invoke(cmd+['-f',str(simple/'pom.xml'),'-Ddistroplane.approve=true',goal+'publish'],ok=False)
    state=json.loads((simple/'.distroplane/fixture-run.json').read_text())
    if state!={'planId':'fixture-plan','runId':'fixture-run','attempts':1,'completed':False}:raise AssertionError(state)
    invoke(cmd+['-f',str(simple/'pom.xml'),'-Ddistroplane.approve=true',goal+'publish'],ok=False)
    if json.loads((simple/'.distroplane/fixture-run.json').read_text())!=state:raise AssertionError('duplicate publication mutated state')
    invoke(cmd+['-f',str(simple/'pom.xml'),goal+'status'])
    (simple/'.fixture-pending').unlink()
    invoke(cmd+['-f',str(simple/'pom.xml'),goal+'resume'])
    result=json.loads((simple/'.distroplane/fixture-run.json').read_text())
    if not result['completed'] or result['attempts'] != 1 or result['planId'] != state['planId'] or result['runId'] != state['runId']:
        raise AssertionError('resume failed to preserve plan/run or republished')
    # Real process timeout after durable state is created: later invocation must resume.
    shutil.rmtree(simple/'.distroplane')
    (simple/'.fixture-hang').touch()
    invoke(cmd+['-f',str(simple/'pom.xml'),'-Ddistroplane.approve=true','-Ddistroplane.timeoutSeconds=1',goal+'publish'],ok=False)
    state=json.loads((simple/'.distroplane/fixture-run.json').read_text())
    (simple/'.fixture-hang').unlink()
    invoke(cmd+['-f',str(simple/'pom.xml'),goal+'resume'])
    restored=json.loads((simple/'.distroplane/fixture-run.json').read_text())
    if state['runId']!=restored['runId'] or state['planId']!=restored['planId'] or restored['attempts']!=1 or not restored['completed']:
        raise AssertionError('cancellation lost original release run')
    # CLI is only a verified bootstrap adapter: corrupted offline bytes refuse execution.
    cli=next(repo.glob('distroplane/cli/0.9.2/*/sha256-*/bin/distroplane*'))
    backup=cli.read_bytes()
    try:
        cli.write_bytes(backup+b'tamper')
        rejected=run(cmd+['-f',str(simple/'pom.xml'),goal+'validate'],ok=False)
        if 'corrupt' not in rejected.lower():raise AssertionError('offline cache did not fail safely')
    finally:cli.write_bytes(backup)
    # Packaged reactor artifacts and source classifier resolve in a single Maven session.
    warm=[args.maven,'-B','-ntp',f'-Dmaven.repo.local={repo}', '-f',str(reactor/'pom.xml'), 'package']
    run(warm, cwd=reactor)
    run(cmd+['-f',str(reactor/'pom.xml'),'package',goal+'validate'], cwd=reactor)
    mapped=reactor/'.distroplane-maven/distroplane.json'
    parsed=json.loads(mapped.read_text())
    for name,suffix in [('app','alpha-1.0.0.jar'),('sources','beta-1.0.0-sources.jar')]:
        resolved=Path(parsed['release']['artifacts'][name]['source'])
        if not resolved.is_file() or not resolved.name.endswith(suffix):raise AssertionError(f'bad reactor artifact selection: {name}={resolved}')
    print('Maven native/offline/cancellation/deduplication/multi-module smoke passed')
if __name__=='__main__':main()
