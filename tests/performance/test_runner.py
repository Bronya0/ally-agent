import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import run


class RunnerTests(unittest.TestCase):
    def setUp(self):
        root = run.ROOT / '.tmp' / 'performance-unit-tests'
        root.mkdir(parents=True, exist_ok=True)
        self.directory = tempfile.TemporaryDirectory(dir=root)
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)

    @staticmethod
    def events(*items):
        return '\n'.join(json.dumps(item) for item in items)

    def test_fragmented_output_and_median(self):
        text = self.events(
            {'Output': 'BenchmarkPerformanceSearchCold/10000-4\t'},
            {'Output': '30 1200000 ns/op 464 B/op 5 allocs/op\n'},
            {'Output': 'BenchmarkPerformanceSearchCold/10000-4 30 1000000 ns/op 460 B/op 5 allocs/op\n'},
            {'Output': 'BenchmarkPerformanceSearchCold/10000-4 30 1400000 ns/op 468 B/op 5 allocs/op\n'},
        )
        data = run.parse_go_output(text)['benchmarks']['BenchmarkPerformanceSearchCold/10000']
        self.assertEqual(data['median']['ns/op'], 1200000)
        self.assertEqual(data['median']['B/op'], 464)
        self.assertEqual(len(data['samples']), 3)

    def test_single_lookup_metric(self):
        text = self.events({'Output': 'BenchmarkPerformanceHistoryCached/1000-4 30 120000 ns/op 0 B/op 0 allocs/op 12 ns/lookup\n'})
        data = run.parse_go_output(text)['benchmarks']['BenchmarkPerformanceHistoryCached/1000']
        self.assertEqual(data['median']['ns/lookup'], 12)

    def test_memory_metric_and_failure_are_preserved(self):
        metric = {'scenario': 'lifecycle_release', 'retained_heap_bytes': -1024}
        parsed = run.parse_go_output(self.events(
            {'Output': 'PERF_METRIC ' + json.dumps(metric) + '\n'},
            {'Action': 'fail', 'Test': 'TestPerformanceLifecycleRelease'},
            {'Action': 'pass', 'Test': 'TestPerformanceLifecycleRelease'},
        ))
        self.assertEqual(parsed['metrics'], [metric])
        self.assertEqual(parsed['tests']['TestPerformanceLifecycleRelease'], 'fail')

    def test_incomplete_samples_and_bad_metrics_are_rejected(self):
        with self.assertRaises(ValueError):
            run.parse_go_output(self.events({'Output': 'BenchmarkPerformanceSnapshotBuild/1000-4 30 1000 ns/op\n'}))
        with self.assertRaises(ValueError):
            run.parse_go_output(self.events({'Output': 'PERF_METRIC {"not_scenario": 1}\n'}))

    def test_no_optional_modes(self):
        with mock.patch('sys.stderr'):
            self.assertEqual(run.main(['--quick']), 2)
            self.assertEqual(run.main(['--focus', 'search']), 2)
            self.assertEqual(run.main(['--compare-ref', 'HEAD']), 2)

    def test_overlay_refuses_existing_targets(self):
        suite = self.root / 'tests/performance'
        (suite / 'desktop').mkdir(parents=True)
        fixture = suite / 'desktop/fixtures_test.go'
        fixture.write_text('//go:build ally_performance\n\npackage desktop\n', encoding='utf-8')
        (self.root / 'desktop').mkdir()
        with mock.patch.object(run, 'ROOT', self.root), mock.patch.object(run, 'SUITE', suite):
            self.assertEqual(run.fixture_overlay(), {str(self.root / 'desktop/zz_ally_performance_fixtures_test.go'): str(fixture)})
            (self.root / 'desktop/zz_ally_performance_fixtures_test.go').write_text('existing', encoding='utf-8')
            with self.assertRaises(ValueError):
                run.fixture_overlay()

    def test_atomic_json_write(self):
        path = self.root / 'results.json'
        run.write_json(path, {'state': 'first'})
        run.write_json(path, {'state': 'complete', 'note': '中文'})
        self.assertEqual(json.loads(path.read_text(encoding='utf-8'))['state'], 'complete')
        self.assertEqual(sorted(p.name for p in self.root.iterdir()), ['results.json'])

    def execute_fixture(self, fail=False, incomplete=False):
        suite = self.root / 'tests/performance'
        (suite / 'desktop').mkdir(parents=True)
        (suite / 'desktop/fixtures_test.go').write_text('fixture', encoding='utf-8')
        (self.root / 'desktop').mkdir()
        calls = []

        def execute(command, log, env, timeout):
            calls.append(command)
            self.assertEqual(env['CGO_ENABLED'], '0')
            self.assertNotIn('ALLY_DATA_DIR', env)
            self.assertNotIn('ALLY_PERF_CONFIG', env)
            self.assertTrue(Path(env['HOME']).is_relative_to(self.root))
            if log.name == 'stress.jsonl':
                log.write_text(self.events({'Action': 'fail' if fail else 'pass', 'Test': 'TestPerformanceSearchStress'}), encoding='utf-8')
                return int(fail), 0.1
            samples = 1 if incomplete else run.REPEATS
            log.write_text(self.events(*[{'Output': 'BenchmarkPerformanceSearchCold/10000-4 30 1000000 ns/op 464 B/op 5 allocs/op\n'} for _ in range(samples)]), encoding='utf-8')
            return 0, 0.1

        with mock.patch.object(run, 'ROOT', self.root), mock.patch.object(run, 'SUITE', suite), \
                mock.patch.object(run.subprocess, 'check_output', side_effect=['head\n', b' M desktop/file.go\n', b'{"GOVERSION":"go1.27.3"}', 'v0.8.1\n', b'{"GOPATH":"cache"}']), \
                mock.patch.object(run, 'execute', side_effect=execute), \
                mock.patch.dict(run.os.environ, {'ALLY_DATA_DIR': 'must-not-leak', 'ALLY_PERF_CONFIG': 'old-mode'}), \
                mock.patch('sys.stdout'), mock.patch('sys.stderr'):
            code = run.main([])
        pointer = json.loads((self.root / '.tmp/performance/latest.json').read_text(encoding='utf-8'))
        path = self.root / pointer['results']
        self.assertFalse(list(path.parent.glob('scratch-*')))
        return code, json.loads(path.read_text(encoding='utf-8')), calls

    def test_full_entry_runs_all_stages_with_fixed_settings(self):
        code, result, calls = self.execute_fixture()
        self.assertEqual(code, 0)
        self.assertEqual(result['status'], 'passed')
        self.assertEqual(len(calls), 2)
        self.assertIn('^TestPerformance', calls[0])
        self.assertIn('^BenchmarkPerformance', calls[1])
        self.assertIn('-benchtime=30x', calls[1])
        self.assertIn('-count=3', calls[1])
        self.assertNotIn('options', result)

    def test_failure_is_reported_without_running_more_tests(self):
        code, result, calls = self.execute_fixture(fail=True)
        self.assertEqual(code, 1)
        self.assertEqual(result['tests']['TestPerformanceSearchStress'], 'fail')
        self.assertEqual(result['status'], 'failed')
        self.assertEqual(len(calls), 1)
        self.assertIn('失败 1', run.report_text(result))

    def test_incomplete_benchmark_cannot_pass(self):
        code, result, _ = self.execute_fixture(incomplete=True)
        self.assertEqual(code, 1)
        self.assertEqual(result['status'], 'failed')

    def test_timeout_kills_owned_process(self):
        process = mock.Mock()
        process.wait.side_effect = subprocess.TimeoutExpired(['go', 'test'], 1)
        with mock.patch.object(run.subprocess, 'Popen', return_value=process), mock.patch.object(run, 'terminate_owned_process') as stop:
            with self.assertRaises(subprocess.TimeoutExpired):
                run.execute(['go', 'test'], self.root / 'timeout.jsonl', {}, 1)
        stop.assert_called_once_with(process)


if __name__ == '__main__':
    unittest.main()
