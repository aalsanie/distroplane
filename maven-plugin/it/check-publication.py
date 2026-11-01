#!/usr/bin/env python3
"""Fail-closed static checks on Sonatype staging and clean consumer contracts."""
import argparse
from pathlib import Path
import re
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
N = '{http://maven.apache.org/POM/4.0.0}'

def run():
    args=argparse.ArgumentParser()
    args.add_argument('--release-version')
    args.add_argument('--resolved',action='store_true')
    args.add_argument('--print-cli-version',action='store_true')
    a=args.parse_args()
    pom=ET.parse(ROOT/'maven-plugin/pom.xml').getroot()
    def node(key):
        text=pom.findtext(N+key)
        assert text, f'missing Maven Central required {key}'
        return text
    assert node('groupId')=='io.github.aalsanie'
    assert node('artifactId')=='distroplane-maven-plugin'
    assert node('packaging')=='maven-plugin'
    version=node('version')
    source=(ROOT/'maven-plugin/src/main/java/io/github/aalsanie/distroplane/maven/AbstractDistroplaneMojo.java').read_text()
    found=re.search(r'property = "distroplane.cliVersion", defaultValue = "([0-9]+\.[0-9]+\.[0-9]+)"', source)
    assert found, 'missing explicit, pinned native CLI default'
    cli_version=found.group(1)
    if a.print_cli_version:
        print(cli_version)
        return
    assert cli_version == (ROOT/'VERSION').read_text().strip(), 'plugin CLI default must match prepared release'
    if a.release_version:
        assert re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+',a.release_version) and a.release_version!='0.0.0'
        if a.resolved:assert version == a.release_version, 'POM artifact version must match release tag'
        else:assert version == a.release_version+'-SNAPSHOT', 'Source snapshot must match signed publication version'
    assert pom.findtext(N+'properties/'+N+'jackson.version')=='2.18.12', 'Jackson must include current security patches'
    management=pom.find(N+'dependencyManagement/'+N+'dependencies')
    bom=next((x for x in management.findall(N+'dependency') if x.findtext(N+'artifactId')=='jackson-bom'), None)
    assert bom is not None and bom.findtext(N+'version')=='${jackson.version}' and bom.findtext(N+'scope')=='import'
    direct={x.findtext(N+'artifactId'):x for x in pom.findall(N+'dependencies/'+N+'dependency')}
    assert 'jackson-databind' in direct and direct['jackson-databind'].find(N+'version') is None
    assert direct['commons-compress'].findtext(N+'version')=='1.28.0'
    enforcer=next((x for x in pom.findall(N+'build/'+N+'plugins/'+N+'plugin') if x.findtext(N+'artifactId')=='maven-enforcer-plugin'),None)
    assert enforcer is not None and enforcer.findtext(N+'version')=='3.6.2'
    rules=enforcer.find(N+'configuration/'+N+'rules')
    assert rules.find(N+'bannedDependencies') is not None and rules.find(N+'dependencyConvergence') is not None
    bans=[x.text for x in rules.findall(N+'bannedDependencies/'+N+'excludes/'+N+'exclude')]
    assert 'com.fasterxml.jackson.core:*:[0,2.18.12)' in bans
    assert 'org.apache.commons:commons-compress:[0,1.28.0)' in bans
    assert (ROOT/'maven-plugin/it/simple/pom.xml').read_text().find('<cliVersion>') < 0
    assert (ROOT/'maven-plugin/it/reactor/pom.xml').read_text().find('<cliVersion>') < 0
    workflow_policy=(ROOT/'.github/workflows/maven-plugin.yml').read_text()
    assert 'check_dependency_tree.py' in workflow_policy and '--osv' in workflow_policy
    assert 'package-ecosystem: maven' in (ROOT/'.github/dependabot.yml').read_text()
    assert node('name') and node('description') and node('url')
    assert pom.find(N+'licenses/'+N+'license/'+N+'name') is not None
    assert pom.find(N+'developers/'+N+'developer/'+N+'name') is not None
    assert pom.find(N+'scm/'+N+'url') is not None
    assert pom.findtext(N+'prerequisites/'+N+'maven')=='3.9.0'
    profiles={p.findtext(N+'id'):p for p in pom.findall(N+'profiles/'+N+'profile')}
    assert 'central-release' in profiles
    plugins=profiles['central-release'].findall(N+'build/'+N+'plugins/'+N+'plugin')
    indexed={p.findtext(N+'artifactId'):p for p in plugins}
    expected={'maven-source-plugin','maven-javadoc-plugin','maven-gpg-plugin','central-publishing-maven-plugin'}
    assert indexed.keys()==expected, 'Central profile must attach exact, signed publication components'
    for name in indexed:
        assert indexed[name].findtext(N+'version')
    stage=indexed['central-publishing-maven-plugin']
    assert stage.findtext(N+'groupId')=='org.sonatype.central'
    config=stage.find(N+'configuration')
    assert config.findtext(N+'autoPublish')=='false', 'CI must not silently publish'
    assert config.findtext(N+'publishingServerId')=='central'
    assert config.findtext(N+'waitUntil')=='validated'
    gpg=indexed['maven-gpg-plugin']
    assert gpg.findtext(N+'executions/'+N+'execution/'+N+'phase')=='verify'
    assert 'sign' in [x.text for x in gpg.findall(N+'executions/'+N+'execution/'+N+'goals/'+N+'goal')]
    assert 'MAVEN_GPG_PASSPHRASE' in ET.tostring(pom,encoding='unicode')
    workflow=(ROOT/'.github/workflows/maven-central-stage.yml').read_text()
    consumer=(ROOT/'.github/workflows/maven-central-consumer.yml').read_text()
    assert 'workflow_dispatch:' in workflow and 'workflow_dispatch:' in consumer
    assert 'push:' not in workflow and 'pull_request:' not in workflow
    assert 'environment: maven-central' in workflow
    assert "inputs.confirmation == 'STAGE'" in workflow
    assert 'git merge-base --is-ancestor HEAD origin/main' in workflow
    assert 'autoPublish>false' not in workflow # configured in profile, not overridden in workflow
    assert '-Pcentral-release' in workflow and ' deploy' in workflow and 'project.build.outputTimestamp' in workflow
    assert 'mvn -B -ntp -U' in consumer
    assert 'maven-central-fresh-' in consumer
    assert 'maven-plugin/examples/npm' in consumer
    assert 'mvn -B -ntp -f maven-plugin/pom.xml install' not in consumer
    print('Maven Central metadata, signing, manual approval, and clean-consumer gates pass')

if __name__=='__main__':run()
