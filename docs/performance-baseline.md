# Performance baseline

This document records a reference performance snapshot for the 0.9.0 release-candidate line. It is not an SLA and does not define pass/fail latency targets. Future measurements should be compared on equivalent hardware and toolchains, with regressions investigated rather than hidden behind fixed thresholds.

## Environment

- Source revision: `47e40cbc21605c7ddaa84fe62a8535b52f789eea`
- Operating system: Ubuntu 24.04 hosted runner, Linux x86-64
- Kernel: Linux 6.17.0-1022-azure
- Go: 1.27.1 linux/amd64
- CPU: Intel Xeon 6973P-C
- Benchmark commands: the `Benchmark record` job in `.github/workflows/ci.yml`

Hosted-runner hardware can vary between runs, so these numbers are a measured reference point rather than a deterministic performance contract.

## Results

| Workload | Time | Memory / throughput | Allocations |
| --- | ---: | ---: | ---: |
| CLI cold process startup | 1.453 ms/op | 18,713 B/op | 35 allocs/op |
| Config decode | 14.483 µs/op | 21.96 MB/s; 8,399 B/op | 134 allocs/op |
| Plan 1 target | 26.889 µs/op | 6,816 B/op | 114 allocs/op |
| Plan 10 targets | 94.092 µs/op | 49,997 B/op | 781 allocs/op |
| Plan 100 targets | 672.837 µs/op | 561,400 B/op | 7,366 allocs/op |
| Plan 1,000 targets | 9.235 ms/op | 5,735,309 B/op | 73,105 allocs/op |
| SHA-256 hash 1 MiB artifact | 1.071 ms/op | 33,672 B/op | 11 allocs/op |
| Scheduler 1,000 operations | 746.924 µs/op | 1,570,184 B/op | 16 allocs/op |
| Scheduler 10,000 operations | 7.764 ms/op | 22,320,600 B/op | 25 allocs/op |
| Execute 1,000 operations resource sample | 32.087 s/op | peak heap 11,054,080 B; peak 67 goroutines; 190,034,248 B/op | 645,114 allocs/op |
| Journal append | 10.814 µs/op | 1,890 B/op | 8 allocs/op |
| Journal read 100,000 events | 234.818 ms/op | 89.24 MB/s; 287,926,362 B/op | 1,233,448 allocs/op |
| Journal reduce 100,000 events | 10.511 ms/op | 3,984 B/op | 20 allocs/op |
| Provider process startup | 1.397 ms/op | 111,580 B/op | 200 allocs/op |

## Interpretation

Planning scales approximately with target count in this snapshot, and the 10,000-operation scheduler remains within a low-millisecond range on the recorded runner. Journal state reduction is inexpensive relative to decoding a 100,000-event journal; journal decoding is the dominant replay allocation cost in this measurement.

The 1,000-operation execution resource sample intentionally exercises the complete executor and durable journal path rather than only scheduler selection. Its reported `B/op` is cumulative allocation traffic; the separately sampled peak heap was about 10.5 MiB and observed goroutines peaked at 67 with executor concurrency bounded to 32. The elapsed time is sensitive to hosted-runner filesystem performance because durable journal writes call sync.

The repository also carries correctness-scale tests for 1,000 targets, a 10,000-operation DAG, 100,000 journal events, repeated waiting-external reconciliation, bounded provider output, cancellation storms, and goroutine release. Those tests are correctness gates; the numbers above are the performance reference.
