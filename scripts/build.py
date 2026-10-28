#!/usr/bin/env python3
"""Shared local/CI release builder. Requires Go and Python 3; macOS for DMG."""
import argparse
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent
DIST = ROOT / 'dist'
RELEASE = DIST / 'release'
WINRES = 'github.com/tc-hib/go-winres@v0.3.3'
RG_TARGETS = {
    ('windows', 'amd64'): 'x86_64-pc-windows-msvc.zip',
    ('linux', 'amd64'): 'x86_64-unknown-linux-musl.tar.gz',
    ('linux', 'arm64'): 'aarch64-unknown-linux-musl.tar.gz',
    ('darwin', 'amd64'): 'x86_64-apple-darwin.tar.gz',
    ('darwin', 'arm64'): 'aarch64-apple-darwin.tar.gz',
}


def run(*args, env=None):
    print('+', ' '.join(map(str, args)), flush=True)
    subprocess.run(list(map(str, args)), cwd=ROOT, env=env, check=True)


def build_env(goos=None, arch=None):
    env = os.environ.copy()
    env['CGO_ENABLED'] = '0'
    env.setdefault('GOMAXPROCS', '2')
    env.setdefault('GOGC', '30')
    if goos:
        env['GOOS'], env['GOARCH'] = goos, arch
    else:
        env.pop('GOOS', None)
        env.pop('GOARCH', None)
    return env


def ripgrep(goos, arch, destination):
    """Extract the vendored binary without Node, network or host-specific tar."""
    matches = list((ROOT / 'tools/ripgrep').glob('ripgrep-*-' + RG_TARGETS[(goos, arch)]))
    if len(matches) != 1:
        raise RuntimeError('Expected one ripgrep archive for ' + goos + '/' + arch)
    archive = matches[0]
    binary = 'rg.exe' if goos == 'windows' else 'rg'
    if archive.suffix == '.zip':
        with zipfile.ZipFile(archive) as z:
            names = [n for n in z.namelist() if n.endswith('/' + binary)]
            if len(names) != 1:
                raise RuntimeError('Missing/ambiguous rg in ' + str(archive))
            data = z.read(names[0])
    else:
        with tarfile.open(archive) as t:
            members = [m for m in t.getmembers() if m.isfile() and m.name.endswith('/' + binary)]
            if len(members) != 1:
                raise RuntimeError('Missing/ambiguous rg in ' + str(archive))
            data = t.extractfile(members[0]).read()
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(data)
    destination.chmod(0o755)


def legal_files(destination):
    destination.mkdir(parents=True, exist_ok=True)
    for name in ('LICENSE', 'THIRD_PARTY_NOTICES.md', 'README.md'):
        shutil.copy2(ROOT / name, destination / name)
    shutil.copytree(ROOT / 'third_party/ripgrep', destination / 'third_party/ripgrep', dirs_exist_ok=True)


def compile_binary(config, goos, arch, destination):
    destination.parent.mkdir(parents=True, exist_ok=True)
    flags = '-s -w -X github.com/egoist/mygo.production=1'
    for field, key in [('packageName', 'name'), ('packageVersion', 'version'), ('packageIdentifier', 'identifier')]:
        flags += ' -X ' + "'github.com/egoist/mygo." + field + '=' + config[key] + "'"
    if goos == 'windows':
        flags += ' -H=windowsgui'
    run('go', 'build', '-trimpath', '-tags', 'mygo_noinspector', '-ldflags', flags,
        '-o', destination, '.', env=build_env(goos, arch))
    destination.chmod(0o755)
    if goos == 'windows':
        numeric = re.match(r'([0-9]+[.][0-9]+[.][0-9]+)', config['version']).group(1) + '.0'
        metadata = {
            'RT_GROUP_ICON': {'APP': {'0000': 'icon.png'}},
            'RT_MANIFEST': {'#1': {'0409': {'minimum-os': 'win10', 'execution-level': 'as invoker',
                                          'dpi-awareness': 'per monitor v2', 'long-path-aware': True}}},
            'RT_VERSION': {'#1': {'0409': {'fixed': {'file_version': numeric, 'product_version': numeric},
                'info': {'0409': {'FileDescription': config['name'], 'ProductName': config['name'],
                    'OriginalFilename': 'ally-native.exe', 'FileVersion': config['version'],
                    'ProductVersion': config['version']}}}}},
        }
        with tempfile.TemporaryDirectory(prefix='ally-winres-') as work:
            spec = Path(work) / 'winres.json'
            shutil.copy2(ROOT / config['icon'], Path(work) / 'icon.png')
            spec.write_text(json.dumps(metadata), encoding='utf-8')
            run('go', 'run', WINRES, 'patch', '--no-backup', '--in', spec, destination, env=build_env())


def verify_archive(archive, goos):
    required = {'LICENSE', 'THIRD_PARTY_NOTICES.md', 'README.md',
                'third_party/ripgrep/LICENSE-MIT', 'third_party/ripgrep/UNLICENSE'}
    if goos == 'windows':
        required |= {'ally-native.exe', 'tools/rg.exe'}
        with zipfile.ZipFile(archive) as z:
            names = set(z.namelist())
            if not required <= names or any('-setup-' in n for n in names) or z.testzip():
                raise RuntimeError('Invalid Windows portable ZIP: ' + str(archive))
            for name in ('ally-native.exe', 'tools/rg.exe'):
                if not z.read(name).startswith(b'MZ'):
                    raise RuntimeError('Not a Windows executable: ' + name)
    else:
        required |= {'ally-native', 'tools/rg'}
        with tarfile.open(archive) as t:
            roots = {m.name.split('/')[0] for m in t.getmembers()}
            if len(roots) != 1:
                raise RuntimeError('Linux archive must contain one root folder')
            root = roots.pop()
            names = {m.name for m in t.getmembers()}
            if not {root + '/' + name for name in required} <= names:
                raise RuntimeError('Incomplete Linux archive: ' + str(archive))
            for name in ('ally-native', 'tools/rg'):
                member = t.getmember(root + '/' + name)
                if not member.mode & 0o111 or t.extractfile(member).read(4) != bytes([127, 69, 76, 70]):
                    raise RuntimeError('Invalid executable or missing execute bits: ' + name)
    print('Verified:', archive, flush=True)


def windows(config, exe_only=False):
    with tempfile.TemporaryDirectory(prefix='ally-windows-', dir=DIST) as work:
        stage = Path(work)
        exe = stage / 'ally-native.exe'
        compile_binary(config, 'windows', 'amd64', exe)
        ripgrep('windows', 'amd64', stage / 'tools/rg.exe')
        if exe_only:
            # Never rename around an in-use fixed executable. os.replace fails clearly.
            try:
                os.replace(exe, DIST / 'ally-native.exe')
            except PermissionError as error:
                raise RuntimeError('dist/ally-native.exe is in use. Close Ally and rerun scripts/build.sh.') from error
            (DIST / 'tools').mkdir(exist_ok=True)
            shutil.copy2(stage / 'tools/rg.exe', DIST / 'tools/rg.exe')
            print('Built:', DIST / 'ally-native.exe')
            return
        legal_files(stage)
        archive = RELEASE / ('Ally-' + config['version'] + '-windows-amd64-portable.zip')
        with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED) as z:
            for path in sorted(stage.rglob('*')):
                if path.is_file():
                    z.write(path, path.relative_to(stage))
        verify_archive(archive, 'windows')


def linux(config):
    for arch in ('amd64', 'arm64'):
        name = 'Ally-' + config['version'] + '-linux-' + arch
        with tempfile.TemporaryDirectory(prefix='ally-linux-', dir=DIST) as work:
            stage = Path(work) / name
            compile_binary(config, 'linux', arch, stage / 'ally-native')
            ripgrep('linux', arch, stage / 'tools/rg')
            legal_files(stage)
            with tarfile.open(RELEASE / (name + '.tar.gz'), 'w:gz') as t:
                def archive_mode(member):
                    executable = member.name in (name + '/ally-native', name + '/tools/rg')
                    member.mode = 0o755 if member.isdir() or executable else 0o644
                    return member
                t.add(stage, arcname=name, filter=archive_mode)
            verify_archive(RELEASE / (name + '.tar.gz'), 'linux')


def verify_universal(binary):
    # lipo 一次只接受一个 -verify_arch：macOS 27 把第二个架构当成第二个输入文件
    for arch in ('x86_64', 'arm64'):
        run('lipo', binary, '-verify_arch', arch)


def macos(config):
    if platform.system() != 'Darwin':
        raise RuntimeError('macOS is required for universal binaries, ad-hoc signing and DMG creation.')
    run('go', 'tool', 'mygo', 'build', '-platform', 'darwin/universal', '-skip-dmg',
        '-skip-notarize', '-sign', '-', '-o', 'dist/native', env=build_env())
    app = DIST / 'native/darwin-universal' / (config['name'] + '.app')
    rg = app / 'Contents/MacOS/tools/rg'
    with tempfile.TemporaryDirectory(prefix='ally-macos-', dir=DIST) as work:
        work = Path(work)
        for arch in ('amd64', 'arm64'):
            ripgrep('darwin', arch, work / arch)
        rg.parent.mkdir(parents=True, exist_ok=True)
        run('lipo', '-create', work / 'amd64', work / 'arm64', '-output', rg)
        rg.chmod(0o755)
        legal_files(app / 'Contents/Resources')
        verify_universal(rg)
        verify_universal(app / 'Contents/MacOS' / config['name'])
        run('codesign', '--force', '--sign', '-', '--timestamp=none', rg)
        run('codesign', '--force', '--deep', '--sign', '-', '--timestamp=none', app)
        run('codesign', '--verify', '--deep', '--strict', app)
        dmg_root = work / 'dmg-root'
        dmg_root.mkdir()
        shutil.copytree(app, dmg_root / app.name, symlinks=True)
        legal_files(dmg_root)
        (dmg_root / 'Applications').symlink_to('/Applications')
        dmg = RELEASE / ('Ally-' + config['version'] + '-macos-universal-adhoc.dmg')
        run('hdiutil', 'create', '-volname', 'Ally', '-srcfolder', dmg_root, '-ov', '-format', 'UDZO', dmg)
        run('hdiutil', 'verify', dmg)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('target', choices=['windows-exe', 'windows', 'linux', 'macos', 'all'])
    args = parser.parse_args()
    config_path = ROOT / 'mygo.json'
    config = json.loads(config_path.read_text(encoding='utf-8'))
    tag = os.environ.get('BUILD_TAG', '')
    match = re.fullmatch(r'v?([0-9]+[.][0-9]+[.][0-9]+(?:[-+][0-9A-Za-z.-]+)?)', tag)
    if match:
        config['version'] = match.group(1)
        config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + chr(10), encoding='utf-8')
    if args.target in ('all', 'macos') and platform.system() != 'Darwin':
        parser.error('This target needs macOS to sign and create the DMG.')
    DIST.mkdir(exist_ok=True)
    (DIST / 'go.mod').write_text('module ally-build-artifacts' + chr(10), encoding='utf-8')
    RELEASE.mkdir(exist_ok=True)
    if args.target in ('windows-exe', 'windows', 'all'):
        windows(config, args.target == 'windows-exe')
    if args.target in ('linux', 'all'):
        linux(config)
    if args.target in ('macos', 'all'):
        macos(config)


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, subprocess.CalledProcessError) as error:
        print('Build failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
