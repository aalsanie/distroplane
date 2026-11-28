#!/usr/bin/env python3
"""Native Maven integration smoke: explicit goals, offline cache, recovery and reactor."""
import argparse
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

def run(command, ok=True, cwd=None):
    print('testing:', ' '.join(command), flush=True)
    p=subprocess.run(command, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=240, cwd=cwd)
    if (p.returncode == 0) != ok:
        print(p.stdout[-8000:])
        raise AssertionError(f'expected success={ok}, return code {p.returncode}')
    return p.stdout

def invocations(project):
    trace=project/'.fixture-cli-invocations'
    return trace.read_text(encoding='utf-8').splitlines() if trace.exists() else []

def assert_pre_cli_failure(output, diagnostic, project, before):
    if diagnostic not in output or invocations(project)!=before:
        raise AssertionError(f'expected pre-CLI refusal: {diagnostic}\n{output[-8000:]}')

def assert_no_publication(project):
    for state in (project/'.distroplane', project/'.distroplane-maven'/'.distroplane'):
        if state.exists():
            raise AssertionError(f'unexpected release state: {state}')

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
    fixture=Path(args.fixture).resolve(strict=True)
    sandbox=tempfile.TemporaryDirectory(prefix='distroplane native smoke ')
    workspace=Path(sandbox.name).resolve()
    simple=workspace/'simple project'
    reactor=workspace/'reactor project'
    shutil.copytree(root/'maven-plugin/it/simple',simple,ignore=shutil.ignore_patterns('target','.distroplane','.distroplane-maven'))
    shutil.copytree(root/'maven-plugin/it/reactor',reactor,ignore=shutil.ignore_patterns('target','.distroplane','.distroplane-maven'))
    for project in (simple, reactor):
        (project/'.fixture-trace').touch()
    original_simple=(simple/'distroplane.json').read_bytes()
    original_reactor=(reactor/'distroplane.json').read_bytes()
    cmd=[args.maven,'-B','-ntp','-o',f'-Dmaven.repo.local={repo}']
    goal='io.github.aalsanie:distroplane-maven-plugin:1.0.1-SNAPSHOT:'
    candidates=list(repo.glob('distroplane/cli/0.9.3/*/sha256-*/bin/distroplane*'))
    if len(candidates)!=1 or candidates[0].read_bytes()!=fixture.read_bytes():
        raise AssertionError('expected exactly one verified controlled native fixture')
    cli=candidates[0]
    backup=cli.read_bytes()
    import functools
    invoke=functools.partial(run, cwd=workspace)
    (simple/'.fixture-pending').unlink(missing_ok=True)
    (simple/'.fixture-hang').unlink(missing_ok=True)
    shutil.rmtree(simple/'.distroplane',ignore_errors=True)
    invoke(cmd+['-f',str(simple/'pom.xml'),'-Ddistroplane.destination=npm','-Ddistroplane.release=v1',goal+'init'])
    invoke(cmd+['-f',str(simple/'pom.xml'),goal+'doctor'])
    invoke(cmd+['-f',str(simple/'pom.xml'),'-Ddistroplane.doctorRemote=true',goal+'doctor'])
    missing=invoke(cmd+['-f',str(simple/'pom.xml'),'-Pdoctor-credential',goal+'doctor'],ok=False)
    if 'MISSING_CREDENTIAL' not in missing or 'Next goal:' not in missing:
        raise AssertionError('missing mapped credential was not reported by doctor')
    invoke(cmd+['-f',str(simple/'pom.xml'),goal+'validate'])
    invoke(cmd+['-f',str(simple/'pom.xml'),goal+'plan'])
    before=invocations(simple)
    unapproved=invoke(cmd+['-f',str(simple/'pom.xml'),goal+'publish'],ok=False)
    assert_pre_cli_failure(unapproved, 'Explicitly select approve=true or an exact confirmPlan', simple, before)
    assert_no_publication(simple)
    (simple/'.fixture-pending').touch()
    pending=invoke(cmd+['-f',str(simple/'pom.xml'),'-Ddistroplane.approve=true',goal+'publish'],ok=False)
    if 'awaiting external acceptance' not in pending or 'code 4' not in pending:
        raise AssertionError('pending Maven result lost native status')
    state=json.loads((simple/'.distroplane/fixture-run.json').read_text())
    if state!={'planId':'fixture-plan','runId':'fixture-run','attempts':1,'completed':False}:raise AssertionError(state)
    duplicate=invoke(cmd+['-f',str(simple/'pom.xml'),'-Ddistroplane.approve=true',goal+'publish'],ok=False)
    if 'RUN_ALREADY_EXISTS' not in duplicate or 'code 3' not in duplicate:
        raise AssertionError('duplicate publication did not preserve native error')
    if json.loads((simple/'.distroplane/fixture-run.json').read_text())!=state:raise AssertionError('duplicate publication mutated state')
    pending_status=invoke(cmd+['-f',str(simple/'pom.xml'),goal+'status'],ok=False)
    if 'code 4' not in pending_status or 'awaiting external acceptance' not in pending_status:
        raise AssertionError('pending Maven status did not preserve non-success exit')
    (simple/'.fixture-pending').unlink()
    invoke(cmd+['-f',str(simple/'pom.xml'),goal+'resume'])
    result=json.loads((simple/'.distroplane/fixture-run.json').read_text())
    if not result['completed'] or result['attempts'] != 1 or result['planId'] != state['planId'] or result['runId'] != state['runId']:
        raise AssertionError('resume failed to preserve plan/run or republished')
    # A settled run is recorded, not proof of external index visibility.
    settled=invoke(cmd+['-f',str(simple/'pom.xml'),goal+'status'])
    if 'recorded as published' not in settled or 'public availability unverified' not in settled:
        raise AssertionError('completed release was not classified as recorded publication')
    # Corrupt state fails closed without deleting or recreating the original run.
    original=(simple/'.distroplane/fixture-run.json').read_bytes()
    try:
        (simple/'.distroplane/fixture-run.json').write_bytes(b'{')
        corrupted=invoke(cmd+['-f',str(simple/'pom.xml'),goal+'status'],ok=False)
        if 'STATE_CORRUPT' not in corrupted or 'code 3' not in corrupted:
            raise AssertionError('corrupt original run was not rejected')
    finally:
        (simple/'.distroplane/fixture-run.json').write_bytes(original)
    if (simple/'.distroplane/fixture-run.json').read_bytes()!=original:
        raise AssertionError('corrupt-state probe changed original release record')
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
    before=invocations(simple)
    try:
        cli.write_bytes(backup+b'tamper')
        rejected=invoke(cmd+['-f',str(simple/'pom.xml'),goal+'validate'],ok=False)
        assert_pre_cli_failure(rejected, 'Manually repair the corrupt verified CLI installation or cache', simple, before)
    finally:cli.write_bytes(backup)
    # Packaged reactor artifacts and source classifier resolve in a single Maven session.
    unbuilt=run(cmd+['-f',str(reactor/'pom.xml'),'-Ddistroplane.approve=true',goal+'publish'],ok=False,cwd=workspace)
    assert_pre_cli_failure(unbuilt, 'Package the selected reactor artifacts and correct artifactMappings', reactor, [])
    assert_no_publication(reactor)
    if (reactor/'.distroplane-maven').exists() or any((reactor/module/'target').exists() for module in ('alpha','beta')):
        raise AssertionError('unbuilt reactor mapping generated configuration or built artifacts')
    warm=[args.maven,'-B','-ntp',f'-Dmaven.repo.local={repo}', '-f',str(reactor/'pom.xml'), 'package']
    run(warm, cwd=workspace)
    if invocations(reactor):
        raise AssertionError('Maven package implicitly invoked Distroplane')
    assert_no_publication(reactor)
    run(cmd+['-f',str(reactor/'pom.xml'),'package',goal+'validate'], cwd=workspace)
    mapped=reactor/'.distroplane-maven/distroplane.json'
    parsed=json.loads(mapped.read_text())
    for name,suffix in [('app','alpha-1.0.0.jar'),('sources','beta-1.0.0-sources.jar')]:
        resolved=Path(parsed['release']['artifacts'][name]['source'])
        if not resolved.is_file() or not resolved.name.endswith(suffix):raise AssertionError(f'bad reactor artifact selection: {name}={resolved}')
    if (simple/'distroplane.json').read_bytes()!=original_simple or (reactor/'distroplane.json').read_bytes()!=original_reactor:
        raise AssertionError('adapter modified source configuration')
    if invocations(reactor)!=['validate']:
        raise AssertionError(f'unexpected reactor CLI invocations: {invocations(reactor)}')
    expected_package=reactor/'alpha/target/alpha-1.0.0.jar'
    if Path(parsed['destinations']['npm']['options']['packagePath'])!=expected_package:
        raise AssertionError('provider package path was not rebased against source configuration')
    assert_no_publication(reactor)
    # Repeat the explicit mapping using the legacy artifact array.
    legacy=json.loads(original_reactor)
    legacy['schemaVersion']='1'
    legacy['release']['artifacts']=[dict(name=name,**value) for name,value in legacy['release']['artifacts'].items()]
    legacy.pop('destinations')
    legacy['targets']=[]
    source=reactor/'distroplane.json'
    source.write_text(json.dumps(legacy),encoding='utf-8')
    original_legacy=source.read_bytes()
    run(cmd+['-f',str(reactor/'pom.xml'),'package',goal+'validate'], cwd=workspace)
    parsed=json.loads(mapped.read_text())
    actual={artifact['name']:Path(artifact['source']) for artifact in parsed['release']['artifacts']}
    if actual!={'app':expected_package,'sources':reactor/'beta/target/beta-1.0.0-sources.jar'} or not all(path.is_file() for path in actual.values()):
        raise AssertionError(f'legacy artifact mapping did not resolve exact packaged artifacts: {actual}')
    if source.read_bytes()!=original_legacy:raise AssertionError('legacy source config changed')
    if invocations(reactor)!=['validate','validate']:
        raise AssertionError(f'unexpected legacy reactor CLI invocations: {invocations(reactor)}')
    assert_no_publication(reactor)
    # A terminal native failure is not a successful build or an authorization to retry.
    shutil.rmtree(simple/'.distroplane')
    (simple/'.fixture-failed').touch()
    failed=invoke(cmd+['-f',str(simple/'pom.xml'),'-Ddistroplane.approve=true',goal+'publish'],ok=False)
    if 'code 6' not in failed or 'failed or cancelled' not in failed:
        raise AssertionError('native failure was not propagated')
    failed_state=(simple/'.distroplane/fixture-run.json').read_bytes()
    for command in ('status','resume','publish'):
        options=['-Ddistroplane.approve=true'] if command=='publish' else []
        invoke(cmd+['-f',str(simple/'pom.xml')]+options+[goal+command],ok=False)
        if (simple/'.distroplane/fixture-run.json').read_bytes()!=failed_state:
            raise AssertionError('terminal failure triggered replacement publication')
    (simple/'.fixture-failed').unlink()
    # Cache and artifact failures must not invoke publication.
    shutil.rmtree(simple/'.distroplane')
    before=invocations(simple)
    try:
        cli.write_bytes(backup+b'tamper')
        rejected=invoke(cmd+['-f',str(simple/'pom.xml'),'-Ddistroplane.approve=true',goal+'publish'],ok=False)
        assert_pre_cli_failure(rejected, 'Manually repair the corrupt verified CLI installation or cache', simple, before)
        assert_no_publication(simple)
    finally:cli.write_bytes(backup)
    sandbox.cleanup()
    print('Maven native/offline/cancellation/deduplication/multi-module smoke passed')
if __name__=='__main__':main()
