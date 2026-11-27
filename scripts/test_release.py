import tempfile
import unittest
from pathlib import Path
from unittest import mock

import release


class PublicationTests(unittest.TestCase):
    tag = 'v1.28.0'
    repo = 'Bronya0/ally-agent'

    def records(self, assets=None):
        return [[{'tag_name': 'v1.27.0', 'draft': False, 'prerelease': False},
                 {'tag_name': self.tag, 'draft': True, 'prerelease': False,
                  'assets': assets or []}]]

    def _with_tag_gate(self, records, tag=None, status='behind', notes=None):
        tag = tag or self.tag
        def fake(*args, **kwargs):
            arg1 = args[1] if len(args) > 1 else ''
            if args[0] == 'api' and arg1.endswith('/commits/' + tag):
                return {'sha': 'tag-sha'}
            if args[0] == 'api' and arg1.endswith('/compare/mygo...tag-sha'):
                return {'status': status}
            if notes is not None and args[:2] == ('release', 'view'):
                return notes
            if args[:2] == ('api', '--paginate'):
                return records
            return fallback
        return fake

    def test_rejects_invalid_older_and_public_tags_before_writing(self):
        for tag in ('mygo', '../v1.28.0', 'v1.28.0-beta.1'):
            with mock.patch.object(release, 'gh') as gh:
                with self.assertRaises(RuntimeError): release.release_context(self.repo, tag)
                gh.assert_not_called()
        for tag in ('v1.26.0', 'v1.27.0'):
            with mock.patch.object(release, 'gh', side_effect=self._with_tag_gate(self.records(), tag=tag)) as gh:
                with self.assertRaises(RuntimeError): release.release_context(self.repo, tag)
                self.assertGreaterEqual(gh.call_count, 1)

    def test_draft_notes_are_preserved(self):
        with tempfile.TemporaryDirectory() as directory:
            with mock.patch.object(release, 'ROOT', Path(directory)), mock.patch.object(release, 'gh', side_effect=self._with_tag_gate(self.records(), notes='## 中文更新说明\n')):
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
                list_calls=0
                def gh(*args, **kwargs):
                    nonlocal list_calls
                    calls.append(args)
                    arg1 = args[1] if len(args) > 1 else ''
                    if args[0] == 'api' and arg1.endswith('/commits/' + self.tag):
                        return {'sha': 'tag-sha'}
                    if args[0] == 'api' and arg1.endswith('/compare/mygo...tag-sha'):
                        return {'status': 'behind'}
                    if args[:2] == ('api', '--paginate'):
                        list_calls += 1
                        return self.records(assets if list_calls > 1 else None)
                    return ''
                with mock.patch.object(release, 'ROOT', root), mock.patch.object(release, 'gh', side_effect=gh):
                    if broken:
                        with self.assertRaises(RuntimeError): release.publish(self.repo,self.tag)
                    else: release.publish(self.repo,self.tag)
                edits=[c for c in calls if c[:2]==('release','edit')]
                self.assertFalse(any(c[:2]==('api','repos/'+self.repo+'/releases/tags/'+self.tag) for c in calls))
                self.assertEqual(bool(edits), broken is None)
                if edits:
                    self.assertEqual(calls[-1], edits[0])
                    self.assertIn('--draft=false',edits[0])
    def test_rejects_tag_outside_mygo_history(self):
        for status in ('diverged', 'ahead'):
            with self.subTest(status=status):
                with mock.patch.object(release, 'gh', side_effect=self._with_tag_gate([], status=status)) as gh:
                    with self.assertRaises(RuntimeError):
                        release.verify_tag_on_branch(self.repo, self.tag)
                    kinds = [c.args[1] for c in gh.call_args_list if len(c.args) > 1 and isinstance(c.args[1], str)]
                    self.assertTrue(any(k.endswith('/commits/' + self.tag) for k in kinds))
                    self.assertTrue(any('/compare/mygo...tag-sha' in k for k in kinds))

    def test_accepts_tag_on_mygo_history(self):
        for status in ('behind', 'identical'):
            with self.subTest(status=status):
                with mock.patch.object(release, 'gh', side_effect=self._with_tag_gate([], status=status)) as gh:
                    release.verify_tag_on_branch(self.repo, self.tag)
                    self.assertGreaterEqual(gh.call_count, 2)


if __name__ == '__main__':
    unittest.main()
