"""Benchmark evidence contracts: missing measurements cannot pass silently."""
import importlib.util
from pathlib import Path
import unittest

module = Path(__file__).with_name('compare-benchmarks.py')
spec = importlib.util.spec_from_file_location('compare_benchmarks', module)
compare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compare)


class BenchmarkCompareTest(unittest.TestCase):
    def row(self, name, value, iterations=3):
        peak = ' 67 peak-goroutines 11054080 peak-heap-B' if name == compare.PEAK else ''
        return f'{name}-16    {iterations}    {int(value)} ns/op{peak}    123 B/op    3 allocs/op'

    def sample(self, multiplier=1):
        return '\n'.join(self.row(name, compare.BASELINE_NS.get(name, 100) * multiplier)
                         for name in compare.REQUIRED)

    def test_complete_record(self):
        rows = compare.compare(self.sample())['benchmarks']
        self.assertEqual(set(rows), set(compare.REQUIRED))
        self.assertTrue(all(rows[name]['ratio'] == 1 for name in compare.BASELINE_NS))

    def test_detects_regression_without_automatic_acceptance(self):
        output = compare.compare(self.sample(3))
        self.assertTrue(output['comparison_requires_approval'])
        self.assertTrue(all(output['benchmarks'][name]['review'] == 'REGRESSION_REVIEW' for name in compare.BASELINE_NS))

    def test_rejects_missing_measurement(self):
        with self.assertRaisesRegex(ValueError, 'missing benchmark evidence'):
            compare.compare(self.row('BenchmarkColdProcessStartup', 123))

    def test_ignores_benchmark_noise(self):
        result = compare.parse_samples('goos: linux\n' + self.row('BenchmarkBuildTargets/1000', 123) + '\nPASS\n')
        self.assertEqual(result['BenchmarkBuildTargets/1000'][0]['metrics']['ns/op'], 123)

    def test_uses_median_of_repetitions(self):
        readings = self.sample() + '\n' + self.sample(2) + '\n' + self.sample(100)
        rows = compare.compare(readings)['benchmarks']
        self.assertTrue(all(rows[name]['ratio'] == 2 for name in compare.BASELINE_NS))


    def test_rejects_each_missing_measurement(self):
        lines = self.sample().splitlines()
        for i, name in enumerate(compare.REQUIRED):
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, 'missing benchmark evidence'):
                compare.compare('\n'.join(lines[:i] + lines[i+1:]))

    def test_rejects_invalid_metrics(self):
        row = self.row(compare.JOURNAL_READ, 123)
        for bad in (row.replace('123 ns/op', 'NaN ns/op'), row.replace('123 ns/op', '0 ns/op'),
                    row.replace('123 B/op', '-1 B/op'), row.replace('3 allocs/op', ''),
                    self.row(compare.JOURNAL_READ, 123, 0),
                    self.row(compare.PEAK, 123).replace('67 peak-goroutines', '0 peak-goroutines'),
                    row + ' 1 ns/op'):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                compare.parse_samples(bad)

    def test_same_runner_reference(self):
        name = compare.JOURNAL_READ
        candidate = self.sample() + '\n' + '\n'.join([self.row(name, 300), self.row(name, 400)])
        reference = '\n'.join(self.row(name, x) for x in (100, 150, 200))
        record = compare.compare(candidate, reference)['same_runner_journal_read']
        self.assertEqual(record['candidate_ns_op'], 400)
        self.assertEqual(record['reference_ns_op'], 150)
        self.assertEqual(record['review'], 'REGRESSION_REVIEW')
        with self.assertRaisesRegex(ValueError, 'same-runner'):
            compare.compare(candidate, self.row(name, 100))

    def test_binary_sizes_are_complete_positive_and_unique(self):
        lines = [f'{name} 100' for name in compare.BINARIES]
        self.assertEqual(compare.binary_sizes('\n'.join(lines)), dict.fromkeys(compare.BINARIES, 100))
        for invalid in (lines[1:], lines + lines[:1], ['distroplane 0'] + lines[1:]):
            with self.assertRaises(ValueError):
                compare.binary_sizes('\n'.join(invalid))


if __name__ == '__main__':
    unittest.main()
