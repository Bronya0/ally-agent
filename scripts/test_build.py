import tempfile
import unittest
import zipfile
from pathlib import Path
from unittest import mock

import build


class WindowsReleaseTests(unittest.TestCase):
    @staticmethod
    def binary(machine):
        data = bytearray(128)
        data[:2] = b'MZ'
        data[0x3c:0x40] = (64).to_bytes(4, 'little')
        data[64:68] = b'PE' + bytes(2)
        data[68:70] = machine.to_bytes(2, 'little')
        return data

    def test_archive_rejects_rg_with_wrong_architecture(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / 'portable.zip'
            with zipfile.ZipFile(archive, 'w') as z:
                for name in ('LICENSE', 'README.md', 'THIRD_PARTY_NOTICES.md',
                             'third_party/ripgrep/LICENSE-MIT', 'third_party/ripgrep/UNLICENSE'):
                    z.writestr(name, 'license')
                z.writestr('Ally.exe', self.binary(0xaa64))
                z.writestr('tools/rg.exe', self.binary(0x8664))
            with self.assertRaisesRegex(RuntimeError, 'Wrong Windows architecture'):
                build.verify_archive(archive, 'windows', 'arm64')


class MacReleaseTests(unittest.TestCase):
    def test_three_architectures_are_thinned_signed_and_packaged(self):
        with tempfile.TemporaryDirectory() as directory:
            dist = Path(directory) / 'dist'
            release = dist / 'release'
            release.mkdir(parents=True)
            calls = []
            config = {'name': 'Ally', 'version': '1.2.3'}

            def fake_run(*args, **kwargs):
                args = tuple(map(str, args))
                calls.append(args)
                if args[:3] == ('go', 'tool', 'mygo'):
                    binary = dist / 'native/darwin-universal/Ally.app/Contents/MacOS/Ally'
                    binary.parent.mkdir(parents=True)
                    binary.write_bytes(b'original')
                elif args[0] == 'lipo' and '-output' in args:
                    Path(args[args.index('-output') + 1]).write_bytes(b'macho')
                elif args[:2] == ('hdiutil', 'create'):
                    Path(args[-1]).write_bytes(b'dmg')

            def fake_rg(system, arch, path):
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b'macho')

            def fake_legal(path):
                path.mkdir(parents=True, exist_ok=True)
                (path / 'LICENSE').write_text('license')

            def fake_archs(args, **kwargs):
                return 'x86_64' if 'package-amd64' in Path(args[-1]).parts else 'arm64'

            with mock.patch.object(build, 'DIST', dist), mock.patch.object(build, 'RELEASE', release), \
                    mock.patch.object(build.platform, 'system', return_value='Darwin'), \
                    mock.patch.object(build, 'run', side_effect=fake_run), \
                    mock.patch.object(build, 'ripgrep', side_effect=fake_rg), \
                    mock.patch.object(build, 'legal_files', side_effect=fake_legal), \
                    mock.patch.object(Path, 'symlink_to'), \
                    mock.patch.object(build.subprocess, 'check_output', side_effect=fake_archs):
                build.macos(config)
            self.assertEqual({p.name for p in release.iterdir()}, {
                'Ally-1.2.3-macos-universal.dmg',
                'Ally-1.2.3-macos-x64.dmg',
                'Ally-1.2.3-macos-arm64.dmg',
            })
            thin_calls = [a for a in calls if a[0] == 'lipo' and '-thin' in a]
            self.assertEqual(len(thin_calls), 4)
            for arch in ('x86_64', 'arm64'):
                self.assertEqual(sum(a[a.index('-thin') + 1] == arch for a in thin_calls), 2)
            app_signs = [a for a in calls if a[0] == 'codesign' and '--deep' in a and '--force' in a]
            self.assertEqual(len(app_signs), 3)
            self.assertEqual(len([a for a in calls if a[:2] == ('hdiutil', 'verify')]), 3)



class LegacyReleaseCompatibilityTests(unittest.TestCase):
    def test_tag_prefix_and_bundle_identity_match_main(self):
        import json
        config = json.loads((build.ROOT / 'mygo.json').read_text(encoding='utf-8'))
        self.assertEqual(config['name'], 'Ally')
        self.assertEqual(config['identifier'], 'io.github.bronya0.ally')
        for tag in ('v1.27.0', '1.27.0'):
            with mock.patch.dict(build.os.environ, {'BUILD_TAG': tag}):
                self.assertEqual(build.release_tag({'version':'1.27.0'}), tag)

class LauncherIntegrityTests(unittest.TestCase):
    def test_launchers_have_no_hidden_control_characters(self):
        for name in ('build.bat', 'build.command', 'build-all.sh'):
            data = (build.ROOT / 'scripts' / name).read_bytes()
            bad = [value for value in data if value < 32 and value not in (9, 10, 13)]
            self.assertEqual(bad, [], name + ' contains control characters')
        batch = (build.ROOT / 'scripts/build.bat').read_text()
        self.assertIn('py -3 "scripts/build.py" dev', batch)
        self.assertIn('python "scripts/build.py" dev', batch)


class DevelopmentBuildTests(unittest.TestCase):
    def test_windows_development_uses_host_architecture(self):
        for machine, arch in [('AMD64', 'amd64'), ('ARM64', 'arm64')]:
            with mock.patch.object(build.platform, 'machine', return_value=machine),                     mock.patch.object(build, 'windows_arch') as compile_arch:
                build.windows({'name': 'Ally'}, exe_only=True)
                compile_arch.assert_called_once_with({'name': 'Ally'}, arch, True)

    def test_local_macos_build_has_rg_and_signature_without_dmg(self):
        with tempfile.TemporaryDirectory() as directory:
            dist = Path(directory)
            target = dist / 'Ally.app'
            target.mkdir()
            (target / 'old-build').write_text('old')
            calls = []

            def fake_run(*args, **kwargs):
                args = tuple(map(str, args))
                calls.append(args)
                if args[:3] == ('go', 'tool', 'mygo'):
                    output = Path(args[args.index('-o') + 1])
                    binary = output / 'darwin-arm64/Ally.app/Contents/MacOS/Ally'
                    binary.parent.mkdir(parents=True)
                    binary.write_bytes(b'macho')

            def fake_rg(system, arch, path):
                self.assertEqual((system, arch), ('darwin', 'arm64'))
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b'rg')

            with mock.patch.object(build, 'DIST', dist),                     mock.patch.object(build.platform, 'machine', return_value='arm64'),                     mock.patch.object(build, 'run', side_effect=fake_run),                     mock.patch.object(build, 'ripgrep', side_effect=fake_rg),                     mock.patch.object(build, 'legal_files'):
                build.macos_app({'name': 'Ally'})
            self.assertFalse((target / 'old-build').exists())
            self.assertEqual((target / 'Contents/MacOS/tools/rg').read_bytes(), b'rg')
            self.assertTrue(any(a[0] == 'codesign' and '--verify' in a for a in calls))
            self.assertFalse(any(a[0] in ('lipo', 'hdiutil') for a in calls))
            self.assertIn('darwin/arm64', calls[0])
            self.assertIn('-skip-dmg', calls[0])

    def test_release_rejects_non_macos_before_writing_files(self):
        with mock.patch.object(build.sys, 'argv', ['build.py', 'release']),                 mock.patch.object(build.platform, 'system', return_value='Windows'),                 mock.patch.object(build, 'windows') as windows:
            with self.assertRaises(SystemExit) as result:
                build.main()
            self.assertEqual(result.exception.code, 2)
            windows.assert_not_called()


if __name__ == '__main__':
    unittest.main()
