#!/usr/bin/env python3
"""Prepare a draft, then publish only after all seven upgrade assets exist."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parent.parent
SUFFIXES = ('windows-x64.zip', 'windows-arm64.zip',
            'macos-universal.dmg', 'macos-x64.dmg', 'macos-arm64.dmg',
            'linux-x64.tar.gz', 'linux-arm64.tar.gz')


def version(tag):
    match = re.fullmatch(r'v?([0-9]+)\.([0-9]+)\.([0-9]+)', tag)
    if not match:
        raise RuntimeError('Use a stable numeric release tag, for example v1.28.0.')
    return tuple(map(int, match.groups()))


def asset_names(tag):
    version(tag)
    return ['Ally-' + tag + '-' + suffix for suffix in SUFFIXES]


def gh(*args, data=False):
    output = subprocess.check_output(['gh', *map(str, args)], cwd=ROOT, text=True, encoding='utf-8')
    return json.loads(output) if data else output


def release_context(repo, tag):
    target = version(tag)
    pages = gh('api', '--paginate', '--slurp', 'repos/' + repo + '/releases?per_page=100', data=True)
    existing = None
    for release in (r for page in pages for r in page):
        if release['tag_name'] == tag:
            existing = release
            if not release['draft']:
                raise RuntimeError('Release is already public; never replace assets being downloaded by clients.')
        if release['draft'] or release['prerelease']:
            continue
        try:
            published = version(release['tag_name'])
        except RuntimeError:
            continue
        if target <= published:
            raise RuntimeError('The release version must be newer than every published stable version.')
    return existing


def prepare(repo, tag):
    if release_context(repo, tag) is None:
        gh('release', 'create', tag, '--repo', repo, '--verify-tag', '--draft',
           '--generate-notes', '--title', 'Ally ' + tag)
    notes = gh('release', 'view', tag, '--repo', repo, '--json', 'body', '--jq', '.body')
    path = ROOT / '.tmp/release-notes.md'
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(notes, encoding='utf-8')
    print('Prepared draft:', tag)


def publish(repo, tag):
    names = asset_names(tag)
    paths = [ROOT / 'dist/release' / name for name in names]
    if any(not p.is_file() or p.stat().st_size == 0 for p in paths):
        raise RuntimeError('All seven non-empty release packages are required before uploading.')
    if release_context(repo, tag) is None:
        raise RuntimeError('Prepare the draft before publishing.')
    gh('release', 'upload', tag, '--repo', repo, '--clobber', *paths)
    release = gh('api', 'repos/' + repo + '/releases/tags/' + tag, data=True)
    if not release['draft'] or release['tag_name'] != tag:
        raise RuntimeError('Release changed while uploading; leave publication to the operator.')
    assets = {a['name']: a for a in release['assets']}
    for path in paths:
        asset = assets.get(path.name, {})
        if asset.get('size') != path.stat().st_size or asset.get('state') != 'uploaded':
            raise RuntimeError('Uploaded asset is missing or incomplete: ' + path.name)
    release_context(repo, tag)
    gh('release', 'edit', tag, '--repo', repo, '--draft=false', '--prerelease=false', '--latest')
    print('Published complete release:', tag)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=['prepare', 'publish'])
    parser.add_argument('tag')
    args = parser.parse_args()
    repo = os.environ.get('GITHUB_REPOSITORY', '')
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repo):
        parser.error('GITHUB_REPOSITORY is required.')
    {'prepare': prepare, 'publish': publish}[args.operation](repo, args.tag)


if __name__ == '__main__':
    main()
