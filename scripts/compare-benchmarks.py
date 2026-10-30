#!/usr/bin/env python3
"""Compare Go benchmark samples with a documented historical reference.

Measurements are advisory across different hardware; nonzero exits mean missing
or malformed evidence, not an automatic performance or release signoff.
"""
import argparse
import json
import re
from pathlib import Path

# Historical Ubuntu x86-64 baseline recorded in docs/performance-baseline.md.
# These ns/op numbers are comparisons, never runtime service-level guarantees.
REFERENCE_COMMIT = '47e40cbc21605c7ddaa84fe62a8535b52f789eea'
BASELINE_NS = {
    'BenchmarkColdProcessStartup': 1_453_000,
    'BenchmarkBuildTargets/1000': 9_235_000,
    'BenchmarkSchedulerTenThousandOperations': 7_764_000,
    'BenchmarkReadOneHundredThousandEvents': 234_818_000,
    'BenchmarkReduceOneHundredThousandEvents': 10_511_000,
    'BenchmarkProviderProcessStartup': 1_397_000,
}
PATTERN = re.compile(r'^(Benchmark\S+?)\s+\d+\s+([\d.]+)\s+ns/op(?:\s|$)', re.MULTILINE)


def parse_samples(output):
    samples = {}
    for match in PATTERN.finditer(output):
        name = re.sub(r'-\d+$', '', match.group(1))
        samples.setdefault(name, []).append(float(match.group(2)))
    return samples


def compare(output):
    samples = parse_samples(output)
    missing = sorted(set(BASELINE_NS) - samples.keys())
    if missing:
        raise ValueError('missing benchmark evidence: ' + ', '.join(missing))
    records = {}
    for name, reference in BASELINE_NS.items():
        readings = sorted(samples[name])
        median = readings[len(readings) // 2] if len(readings) % 2 else (readings[len(readings)//2-1] + readings[len(readings)//2]) / 2
        ratio = median / reference
        records[name] = {'baseline_ns_op': reference, 'observed_ns_op': median,
                         'samples': len(readings), 'ratio': round(ratio, 3),
                         'review': 'REGRESSION_REVIEW' if ratio > 2 else 'RECORDED'}
    return {'baseline_commit': REFERENCE_COMMIT, 'measurement_basis': 'hosted-runner comparison only',
            'comparison_requires_approval': True, 'benchmarks': records}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--environment', type=Path, required=True)
    args = parser.parse_args()
    report = compare(args.input.read_text(encoding='utf-8'))
    report['commit'] = args.commit
    report['environment'] = args.environment.read_text(encoding='utf-8').strip()
    args.output.write_text(json.dumps(report, indent=2, sort_keys=True) + '\n', encoding='utf-8')
    print('Benchmarks compared with reference:', report['baseline_commit'])
    for name, row in report['benchmarks'].items():
        print(f"{name}: {row['ratio']:.3f}x ({row['review']})")


if __name__ == '__main__':
    main()
