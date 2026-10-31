#!/usr/bin/env python3
"""Shared local/CI release builder. Requires Go and Python 3; macOS for DMG."""
import argparse
from contextlib import contextmanager
import json
import os
from pathlib import Path
import platform
import re
import shutil
import struct
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
    ('windows', 'arm64'): 'aarch64-pc-windows-msvc.zip',
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


@contextmanager
def bundled_release_notes(config, release=False):
    """Embed the release event's Markdown in every platform; never retain it locally."""
    path = ROOT / 'resources/release_notes.json'
    original = path.read_bytes()
    notes = {
        'version': release_tag(config) if release else '',
        'body': os.environ.get('ALLY_RELEASE_NOTES_BODY', '').strip() if release else '',
    }
    try:
        path.write_text(json.dumps(notes, ensure_ascii=False), encoding='utf-8')
        yield
    finally:
        path.write_bytes(original)


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
            # Numeric ID 1: MyGo's notifications and tray load the icon with LoadIcon(instance, 1);
            # a named "APP" icon has no such ID and Windows shows a generic icon instead.
            'RT_GROUP_ICON': {'#1': {'0000': 'icon.png'}},
            'RT_MANIFEST': {'#1': {'0409': {'minimum-os': 'win10', 'execution-level': 'as invoker',
                                          'dpi-awareness': 'per monitor v2', 'long-path-aware': True}}},
            'RT_VERSION': {'#1': {'0409': {'fixed': {'file_version': numeric, 'product_version': numeric},
                'info': {'0409': {'FileDescription': config['name'], 'ProductName': config['name'],
                    'OriginalFilename': destination.name, 'FileVersion': config['version'],
                    'ProductVersion': config['version']}}}}},
        }
        with tempfile.TemporaryDirectory(prefix='ally-winres-') as work:
            spec = Path(work) / 'winres.json'
            shutil.copy2(ROOT / config['icon'], Path(work) / 'icon.png')
            spec.write_text(json.dumps(metadata), encoding='utf-8')
            run('go', 'run', WINRES, 'patch', '--no-backup', '--in', spec, destination, env=build_env())


def release_tag(config):
    tag = os.environ.get('BUILD_TAG', '')
    return tag if re.fullmatch(r'v?[0-9]+[.][0-9]+[.][0-9]+', tag) else config['version']


def verify_archive(archive, goos, arch=None):
    required = {'LICENSE', 'THIRD_PARTY_NOTICES.md', 'README.md',
                'third_party/ripgrep/LICENSE-MIT', 'third_party/ripgrep/UNLICENSE'}
    if goos == 'windows':
        required |= {'Ally.exe', 'tools/rg.exe'}
        with zipfile.ZipFile(archive) as z:
            names = set(z.namelist())
            if not required <= names or any('-setup-' in n for n in names) or z.testzip():
                raise RuntimeError('Invalid Windows portable ZIP: ' + str(archive))
            for name in ('Ally.exe', 'tools/rg.exe'):
                data = z.read(name)
                if len(data) < 64 or not data.startswith(b'MZ'):
                    raise RuntimeError('Not a Windows executable: ' + name)
                offset = struct.unpack_from('<I', data, 0x3c)[0]
                if offset + 6 > len(data) or data[offset:offset+4] != b'PE' + bytes(2):
                    raise RuntimeError('Invalid PE header: ' + name)
                machine = struct.unpack_from('<H', data, offset + 4)[0]
                if arch and machine != {'amd64': 0x8664, 'arm64': 0xaa64}[arch]:
                    raise RuntimeError('Wrong Windows architecture for ' + name + ': ' + hex(machine))
    else:
        required |= {'Ally', 'tools/rg'}
        with tarfile.open(archive) as t:
            roots = {m.name.split('/')[0] for m in t.getmembers()}
            if len(roots) != 1:
                raise RuntimeError('Linux archive must contain one root folder')
            root = roots.pop()
            names = {m.name for m in t.getmembers()}
            if not {root + '/' + name for name in required} <= names:
                raise RuntimeError('Incomplete Linux archive: ' + str(archive))
            for name in ('Ally', 'tools/rg'):
                member = t.getmember(root + '/' + name)
                if not member.mode & 0o111 or t.extractfile(member).read(4) != bytes([127, 69, 76, 70]):
                    raise RuntimeError('Invalid executable or missing execute bits: ' + name)
    print('Verified:', archive, flush=True)


def windows(config, exe_only=False):
    for arch in ((host_arch(),) if exe_only else ('amd64', 'arm64')):
        windows_arch(config, arch, exe_only)


def windows_arch(config, arch, exe_only=False):
    with tempfile.TemporaryDirectory(prefix='ally-windows-', dir=DIST) as work:
        stage = Path(work)
        exe = stage / 'Ally.exe'
        compile_binary(config, 'windows', arch, exe)
        ripgrep('windows', arch, stage / 'tools/rg.exe')
        if exe_only:
            # Never rename around an in-use fixed executable. os.replace fails clearly.
            try:
                os.replace(exe, DIST / 'ally-native.exe')
            except PermissionError as error:
                raise RuntimeError('dist/ally-native.exe is in use. Close Ally and rerun scripts/build.bat.') from error
            (DIST / 'tools').mkdir(exist_ok=True)
            shutil.copy2(stage / 'tools/rg.exe', DIST / 'tools/rg.exe')
            print('Built:', DIST / 'ally-native.exe')
            return
        legal_files(stage)
        archive = RELEASE / ('Ally-' + release_tag(config) + '-windows-' + ('x64' if arch == 'amd64' else arch) + '.zip')
        with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED) as z:
            for path in sorted(stage.rglob('*')):
                if path.is_file():
                    z.write(path, path.relative_to(stage))
        verify_archive(archive, 'windows', arch)


def linux(config):
    for arch in ('amd64', 'arm64'):
        name = 'Ally-' + release_tag(config) + '-linux-' + ('x64' if arch == 'amd64' else arch)
        with tempfile.TemporaryDirectory(prefix='ally-linux-', dir=DIST) as work:
            stage = Path(work) / name
            compile_binary(config, 'linux', arch, stage / 'Ally')
            ripgrep('linux', arch, stage / 'tools/rg')
            legal_files(stage)
            with tarfile.open(RELEASE / (name + '.tar.gz'), 'w:gz') as t:
                def archive_mode(member):
                    executable = member.name in (name + '/Ally', name + '/tools/rg')
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
        for name, arch in (('universal', None), ('amd64', 'x86_64'), ('arm64', 'arm64')):
            variant_root = work / ('package-' + name)
            variant_root.mkdir()
            variant = variant_root / app.name
            shutil.copytree(app, variant, symlinks=True)
            executable = variant / 'Contents/MacOS' / config['name']
            variant_rg = variant / 'Contents/MacOS/tools/rg'
            if arch:
                for binary in (executable, variant_rg):
                    thin = variant_root / (binary.name + '.thin')
                    run('lipo', binary, '-thin', arch, '-output', thin)
                    thin.chmod(0o755)
                    os.replace(thin, binary)
                    actual = subprocess.check_output(['lipo', '-archs', str(binary)], text=True).split()
                    if actual != [arch]:
                        raise RuntimeError('Incorrect architecture in ' + str(binary) + ': ' + str(actual))
            else:
                verify_universal(executable)
                verify_universal(variant_rg)
            run('codesign', '--force', '--sign', '-', '--timestamp=none', variant_rg)
            run('codesign', '--force', '--deep', '--sign', '-', '--timestamp=none', variant)
            run('codesign', '--verify', '--deep', '--strict', variant)
            dmg_root = variant_root / 'dmg-root'
            dmg_root.mkdir()
            shutil.copytree(variant, dmg_root / variant.name, symlinks=True)
            legal_files(dmg_root)
            (dmg_root / 'Applications').symlink_to('/Applications')
            dmg = RELEASE / ('Ally-' + release_tag(config) + '-macos-' + ('x64' if name == 'amd64' else name) + '.dmg')
            run('hdiutil', 'create', '-volname', 'Ally', '-srcfolder', dmg_root, '-ov', '-format', 'UDZO', dmg)
            run('hdiutil', 'verify', dmg)


def host_arch():
    machine = platform.machine().lower()
    if machine in ('arm64', 'aarch64'):
        return 'arm64'
    if machine in ('amd64', 'x86_64'):
        return 'amd64'
    raise RuntimeError('Unsupported development architecture: ' + machine)


def macos_app(config):
    """Fast local .app for this Mac; no release archives or universal build."""
    arch = host_arch()
    with tempfile.TemporaryDirectory(prefix='ally-dev-mac-', dir=DIST) as directory:
        stage = Path(directory)
        run('go', 'tool', 'mygo', 'build', '-platform', 'darwin/' + arch,
            '-skip-dmg', '-skip-notarize', '-sign', '-', '-o', stage, env=build_env())
        app = stage / ('darwin-' + arch) / (config['name'] + '.app')
        rg = app / 'Contents/MacOS/tools/rg'
        ripgrep('darwin', arch, rg)
        legal_files(app / 'Contents/Resources')
        run('codesign', '--force', '--sign', '-', '--timestamp=none', rg)
        run('codesign', '--force', '--deep', '--sign', '-', '--timestamp=none', app)
        run('codesign', '--verify', '--deep', '--strict', app)
        target = DIST / (config['name'] + '.app')
        if target.is_symlink():
            raise RuntimeError('Refusing to replace symlink: ' + str(target))
        backup = stage / 'previous.app'
        if target.exists():
            os.replace(target, backup)
        try:
            os.replace(app, target)
        except OSError:
            if backup.exists():
                os.replace(backup, target)
            raise
        print('Built:', target, flush=True)


def main():
    if sys.version_info < (3, 9):
        raise RuntimeError('Python 3.9+ is required.')
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('target', choices=['dev', 'release'])
    args = parser.parse_args()
    system = platform.system()
    if args.target == 'release' and system != 'Darwin':
        parser.error('Release packaging requires macOS for signing and DMG creation.')
    if args.target == 'dev' and system not in ('Windows', 'Darwin'):
        parser.error('Local development builds support Windows and macOS.')
    config_path = ROOT / 'mygo.json'
    config = json.loads(config_path.read_text(encoding='utf-8'))
    tag = os.environ.get('BUILD_TAG', '') if args.target == 'release' else ''
    if args.target == 'release' and not re.fullmatch(r'v?[0-9]+[.][0-9]+[.][0-9]+', tag):
        parser.error('Release packaging requires BUILD_TAG (for example v1.28.0); never publish a development version.')
    match = re.fullmatch(r'v?([0-9]+[.][0-9]+[.][0-9]+(?:[-+][0-9A-Za-z.-]+)?)', tag)
    if match:
        config['version'] = match.group(1)
        config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + chr(10), encoding='utf-8')
    DIST.mkdir(exist_ok=True)
    (DIST / 'go.mod').write_text('module ally-build-artifacts' + chr(10), encoding='utf-8')
    with bundled_release_notes(config, release=args.target == 'release'):
        if args.target == 'dev':
            if system == 'Windows':
                windows(config, exe_only=True)
            else:
                macos_app(config)
        else:
            RELEASE.mkdir(exist_ok=True)
            windows(config)
            linux(config)
            macos(config)


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, OSError, subprocess.CalledProcessError) as error:
        print('Build failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
