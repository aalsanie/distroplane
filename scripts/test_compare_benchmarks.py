"""Benchmark evidence contracts: missing measurements cannot pass silently."""
import importlib.util
from pathlib import Path
import unittest

module = Path(__file__).with_name('compare-benchmarks.py')
spec = importlib.util.spec_from_file_location('compare_benchmarks', module)
compare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compare)


class BenchmarkCompareTest(unittest.TestCase):
    def sample(self, multiplier=1):
        return '\n'.join(
            f'{name}-16    3    {int(value * multiplier)} ns/op    123 B/op    3 allocs/op'
            for name, value in compare.BASELINE_NS.items())

    def test_complete_record(self):
        rows = compare.compare(self.sample())['benchmarks']
        self.assertEqual(len(rows), len(compare.BASELINE_NS))
        self.assertTrue(all(row['ratio'] == 1 for row in rows.values()))

    def test_detects_regression_without_automatic_acceptance(self):
        output = compare.compare(self.sample(3))
        self.assertTrue(output['comparison_requires_approval'])
        self.assertTrue(all(r['review'] == 'REGRESSION_REVIEW' for r in output['benchmarks'].values()))

    def test_rejects_missing_measurement(self):
        with self.assertRaisesRegex(ValueError, 'missing benchmark evidence'):
            compare.compare('BenchmarkColdProcessStartup-16 2 123 ns/op')

    def test_ignores_benchmark_noise(self):
        result = compare.parse_samples('goos: linux\nBenchmarkBuildTargets/1000-8 3 123 ns/op\nPASS\n')
        self.assertEqual(result, {'BenchmarkBuildTargets/1000': [123.0]})

    def test_uses_median_of_repetitions(self):
        readings = self.sample() + '\n' + self.sample(2) + '\n' + self.sample(100)
        rows = compare.compare(readings)['benchmarks']
        self.assertTrue(all(row['ratio'] == 2 for row in rows.values()))


if __name__ == '__main__':
    unittest.main()
