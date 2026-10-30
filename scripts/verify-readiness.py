#!/usr/bin/env python3
"""Offline documentation and public-consumer contract checks.

Checks references against the checked-out tree; never makes network requests,
executes shell examples, or treats a pending external catalog as published.
"""
import argparse
import json
import re
from pathlib import Path
from urllib.parse import unquote, urlsplit

MARKDOWN_LINK = re.compile(r'(?<!!)\[[^\]]+\]\(([^\s)]+)(?:\s+"[^"]*")?\)')
CODE_FENCE = re.compile(r'^\s*```')


def markdown_paths(root):
    for base in (root / 'README.md', root / 'SECURITY.md', root / 'CONTRIBUTING.md'):
        if base.is_file():
            yield base
    for directory in ('docs', 'providers', 'maven-plugin'):
        path = root / directory
        if path.is_dir():
            yield from path.rglob('*.md')


def verify_links(root):
    problems = []
    root = root.resolve()
    checked = 0
    for source in markdown_paths(root):
        fenced = False
        for number, line in enumerate(source.read_text(encoding='utf-8').splitlines(), 1):
            if CODE_FENCE.match(line):
                fenced = not fenced
                continue
            if fenced:
                continue
            for match in MARKDOWN_LINK.finditer(line):
                uri = match.group(1)
                parsed = urlsplit(uri)
                if parsed.scheme or parsed.netloc or uri.startswith(('#', 'mailto:')):
                    continue
                if parsed.path.startswith('/'):
                    problems.append(f'{source.relative_to(root)}:{number}: absolute local link {uri}')
                    continue
                destination = (source.parent / unquote(parsed.path)).resolve()
                checked += 1
                if not destination.is_relative_to(root) or not destination.exists():
                    problems.append(f'{source.relative_to(root)}:{number}: broken or escaping link {uri}')
    return checked, problems


def verify_examples(root):
    problems = []
    paths = [root / 'maven-plugin/examples/npm/pom.xml']
    for path in paths:
        if not path.exists():
            problems.append(f'missing {path.relative_to(root)}')
            continue
        text = path.read_text(encoding='utf-8')
        if '<distroplane.plugin.version>1.0.0</distroplane.plugin.version>' not in text:
            problems.append(f'{path.relative_to(root)}: expected verified public Maven plugin version 1.0.0')
    for path in [root / 'maven-plugin/examples/npm/distroplane.json',
                 *sorted((root / 'internal/config/testdata/compatibility').glob('*.json'))]:
        try:
            json.loads(path.read_text(encoding='utf-8'))
        except (OSError, ValueError) as exc:
            problems.append(f'{path.relative_to(root)}: invalid JSON: {exc}')
    return problems


def verify_readiness(root):
    problems = []
    checked, links = verify_links(root)
    problems.extend(links)
    problems.extend(verify_examples(root))
    readme = (root / 'README.md').read_text(encoding='utf-8')
    if 'distroplane init' not in readme or 'distroplane publish' not in readme or 'distroplane resume' not in readme:
        problems.append('README.md: missing high-level init/publish/resume entry points')
    if 'winget install --id Aalsanie.Distroplane' in readme:
        problems.append('README.md: WinGet public catalog must not be advertised until independently verified')
    security = (root / 'docs/security-incident-response.md')
    if not security.is_file():
        problems.append('missing security incident response and withdrawal procedure')
    readiness = root / 'docs/production-readiness.md'
    if not readiness.is_file():
        problems.append('missing production readiness acceptance matrix')
    return checked, problems


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    args = parser.parse_args()
    checked, problems = verify_readiness(args.root)
    for problem in problems:
        print(f'FAIL: {problem}')
    if problems:
        raise SystemExit(f'readiness contract failed: {len(problems)} issue(s), {checked} local links checked')
    print(f'readiness contract passed: {checked} local links checked, examples parse, no premature catalog claim')


if __name__ == '__main__':
    main()
