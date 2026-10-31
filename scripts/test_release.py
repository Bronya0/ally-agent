import tempfile
import unittest
from pathlib import Path
from unittest import mock

import release


class PublicationTests(unittest.TestCase):
    tag = 'v1.28.0'
    repo = 'Bronya0/ally-agent'

    def records(self):
        return [[{'tag_name': 'v1.27.0', 'draft': False, 'prerelease': False},
                 {'tag_name': self.tag, 'draft': True, 'prerelease': False}]]

    def test_rejects_invalid_older_and_public_tags_before_writing(self):
        for tag in ('mygo', '../v1.28.0', 'v1.28.0-beta.1'):
            with mock.patch.object(release, 'gh') as gh:
                with self.assertRaises(RuntimeError): release.release_context(self.repo, tag)
                gh.assert_not_called()
        for tag in ('v1.26.0', 'v1.27.0'):
            with mock.patch.object(release, 'gh', return_value=self.records()) as gh:
                with self.assertRaises(RuntimeError): release.release_context(self.repo, tag)
                self.assertEqual(gh.call_count, 1)

    def test_draft_notes_are_preserved(self):
        with tempfile.TemporaryDirectory() as directory:
            with mock.patch.object(release, 'ROOT', Path(directory)), mock.patch.object(release, 'gh', side_effect=[self.records(), '## 中文更新说明\n']):
                release.prepare(self.repo, self.tag)
                self.assertEqual((Path(directory)/'.tmp/release-notes.md').read_text(encoding='utf-8'), '## 中文更新说明\n')

    def test_missing_architecture_never_uploads(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            folder=root/'dist/release';folder.mkdir(parents=True)
            for name in release.asset_names(self.tag)[:-1]: (folder/name).write_bytes(b'package')
            with mock.patch.object(release, 'ROOT', root), mock.patch.object(release, 'gh') as gh:
                with self.assertRaises(RuntimeError): release.publish(self.repo, self.tag)
                gh.assert_not_called()

    def test_publish_only_after_all_uploads_are_verified(self):
        for broken in (None, 'missing', 'wrong-size', 'unfinished'):
            with self.subTest(broken=broken), tempfile.TemporaryDirectory() as directory:
                root=Path(directory);folder=root/'dist/release';folder.mkdir(parents=True)
                names=release.asset_names(self.tag)
                self.assertIn('Ally-v1.28.0-windows-x64.zip', names)
                self.assertIn('Ally-v1.28.0-macos-universal.dmg', names)
                self.assertEqual(len(names), 7)
                for name in names: (folder/name).write_bytes(b'package')
                assets=[{'name':name,'size':7,'state':'uploaded'} for name in names]
                if broken=='missing': assets.pop()
                if broken=='wrong-size': assets[0]['size']=1
                if broken=='unfinished': assets[0]['state']='starter'
                calls=[]
                def gh(*args, **kwargs):
                    calls.append(args)
                    if args[:2]==('api','--paginate'): return self.records()
                    if args[0]=='api': return {'tag_name':self.tag,'draft':True,'assets':assets}
                    return ''
                with mock.patch.object(release, 'ROOT', root), mock.patch.object(release, 'gh', side_effect=gh):
                    if broken:
                        with self.assertRaises(RuntimeError): release.publish(self.repo,self.tag)
                    else: release.publish(self.repo,self.tag)
                edits=[c for c in calls if c[:2]==('release','edit')]
                self.assertEqual(bool(edits), broken is None)
                if edits:
                    self.assertEqual(calls[-1], edits[0])
                    self.assertIn('--draft=false',edits[0])


if __name__ == '__main__':
    unittest.main()
