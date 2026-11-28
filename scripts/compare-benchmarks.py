#!/usr/bin/env python3
"""Compare Go benchmark samples with a documented historical reference.

Measurements are advisory across different hardware; nonzero exits mean missing
or malformed evidence, not an automatic performance or release signoff.
"""
import argparse
import json
import math
import re
from statistics import median
from pathlib import Path

# Historical Ubuntu x86-64 baseline recorded in docs/performance-baseline.md.
# These ns/op numbers are comparisons, never runtime service-level guarantees.
REFERENCE_COMMIT = '47e40cbc21605c7ddaa84fe62a8535b52f789eea'
BASELINE_NS = {
    'BenchmarkColdProcessStartup': 1_453_000,
    'BenchmarkDecodeConfig': 14_483,
    'BenchmarkBuildTargets/1': 26_889,
    'BenchmarkBuildTargets/10': 94_092,
    'BenchmarkBuildTargets/100': 672_837,
    'BenchmarkFileHasher1MiB': 1_071_000,
    'BenchmarkSchedulerOneThousandOperations': 746_924,
    'BenchmarkExecuteOneThousandOperationsPeakMemory': 32_087_000_000,
    'BenchmarkWriterAppend': 10_814,
    'BenchmarkBuildTargets/1000': 9_235_000,
    'BenchmarkSchedulerTenThousandOperations': 7_764_000,
    'BenchmarkReadOneHundredThousandEvents': 234_818_000,
    'BenchmarkReduceOneHundredThousandEvents': 10_511_000,
    'BenchmarkProviderProcessStartup': 1_397_000,
}
UNBASELINED = ('BenchmarkReadTenThousandEvents', 'BenchmarkReduceTenThousandEvents')
REQUIRED = tuple(BASELINE_NS) + UNBASELINED
JOURNAL_READ = 'BenchmarkReadOneHundredThousandEvents'
PEAK = 'BenchmarkExecuteOneThousandOperationsPeakMemory'
PATTERN = re.compile(r'^(Benchmark\S+)\s+(\d+)\s+(.+)$', re.MULTILINE)
BINARIES = ('distroplane', 'distroplane-provider-npm', 'distroplane-provider-sdkman',
            'distroplane-provider-homebrew', 'distroplane-provider-winget')


def parse_samples(output):
    samples = {}
    for match in PATTERN.finditer(output):
        name = re.sub(r'-\d+$', '', match.group(1))
        if name not in REQUIRED:
            continue
        fields = match.group(3).split()
        iterations = int(match.group(2))
        if iterations <= 0 or len(fields) % 2:
            raise ValueError('malformed benchmark evidence: ' + name)
        metrics = {}
        for value, unit in zip(fields[::2], fields[1::2]):
            number = float(value)
            if not math.isfinite(number) or number < 0 or unit in metrics:
                raise ValueError('invalid benchmark metric: ' + name)
            metrics[unit] = number
        required = {'ns/op', 'B/op', 'allocs/op'}
        if name == PEAK:
            required.update(('peak-heap-B', 'peak-goroutines'))
        if not required <= metrics.keys() or metrics['ns/op'] <= 0:
            raise ValueError('missing or invalid benchmark metrics: ' + name)
        if name == PEAK and any(metrics[unit] <= 0 for unit in ('peak-heap-B', 'peak-goroutines')):
            raise ValueError('invalid peak resource measurement: ' + name)
        samples.setdefault(name, []).append({'iterations': iterations, 'metrics': metrics})
    return samples


def compare(output, reference_output=None):
    samples = parse_samples(output)
    missing = sorted(set(REQUIRED) - samples.keys())
    if missing:
        raise ValueError('missing benchmark evidence: ' + ', '.join(missing))
    records = {}
    for name in REQUIRED:
        readings = samples[name]
        observed = median(row['metrics']['ns/op'] for row in readings)
        reference = BASELINE_NS.get(name)
        ratio = observed / reference if reference is not None else None
        records[name] = {
            'baseline_ns_op': reference, 'observed_ns_op': observed,
            'samples': len(readings), 'measurements': readings,
            'ratio': round(ratio, 3) if ratio is not None else None,
            'review': ('REGRESSION_REVIEW' if ratio > 2 else 'RECORDED') if ratio is not None else 'NO_BASELINE',
        }
    report = {'baseline_commit': REFERENCE_COMMIT, 'measurement_basis': 'hosted-runner comparison only',
              'comparison_requires_approval': True, 'benchmarks': records}
    if reference_output is not None:
        reference = parse_samples(reference_output).get(JOURNAL_READ, [])
        candidate = samples[JOURNAL_READ]
        if len(reference) != 3 or len(candidate) != 3 or any(row['iterations'] != 3 for row in reference + candidate):
            raise ValueError('same-runner journal comparison requires three 3-iteration samples per revision')
        baseline = median(row['metrics']['ns/op'] for row in reference)
        ratio = records[JOURNAL_READ]['observed_ns_op'] / baseline
        report['same_runner_journal_read'] = {
            'reference_ns_op': baseline, 'reference_measurements': reference,
            'candidate_ns_op': records[JOURNAL_READ]['observed_ns_op'],
            'ratio': round(ratio, 3), 'review': 'REGRESSION_REVIEW' if ratio > 2 else 'RECORDED',
        }
    return report


def binary_sizes(output):
    sizes = {}
    for line in output.splitlines():
        name, size = line.split()
        if name not in BINARIES or name in sizes or not size.isdecimal() or int(size) <= 0:
            raise ValueError('invalid binary size record: ' + line)
        sizes[name] = int(size)
    if set(sizes) != set(BINARIES):
        raise ValueError('missing native binary size evidence')
    return sizes


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', type=Path, required=True)
    parser.add_argument('--reference-input', type=Path, required=True)
    parser.add_argument('--binary-sizes', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--environment', type=Path, required=True)
    args = parser.parse_args()
    if not re.fullmatch(r'[0-9a-f]{40}', args.commit):
        raise ValueError('invalid candidate commit')
    report = compare(args.input.read_text(encoding='utf-8'), args.reference_input.read_text(encoding='utf-8'))
    report['commit'] = args.commit
    report['environment'] = args.environment.read_text(encoding='utf-8').strip()
    if not report['environment']:
        raise ValueError('missing benchmark environment')
    report['native_binary_sizes_bytes'] = binary_sizes(args.binary_sizes.read_text(encoding='utf-8'))
    args.output.write_text(json.dumps(report, indent=2, sort_keys=True) + '\n', encoding='utf-8')
    print('Benchmarks compared with reference:', report['baseline_commit'])
    for name, row in report['benchmarks'].items():
        print(f"{name}: {row['ratio']}x ({row['review']})" if row['ratio'] is not None else f"{name}: {row['review']}")
    paired = report['same_runner_journal_read']
    print(f"Same-runner journal read: {paired['ratio']:.3f}x ({paired['review']})")


if __name__ == '__main__':
    main()
