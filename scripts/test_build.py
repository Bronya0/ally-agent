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

if __name__ == '__main__':
    unittest.main()
