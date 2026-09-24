# Performance baseline

Reference measurements for the 0.9.0 release-candidate line. Hosted-runner hardware varies; these numbers are comparison points, not latency guarantees.

## Environment

- Source revision: `47e40cbc21605c7ddaa84fe62a8535b52f789eea`
- Ubuntu 24.04 hosted runner, Linux x86-64
- Linux 6.17.0-1022-azure
- Go 1.27.1 linux/amd64
- Intel Xeon 6973P-C
- Commands: `Benchmark record` job in `.github/workflows/ci.yml`

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
| Execute 1,000 operations | 32.087 s/op | peak heap 11,054,080 B; peak 67 goroutines; 190,034,248 B/op | 645,114 allocs/op |
| Journal append, in-memory stub | 10.814 µs/op | 1,890 B/op | 8 allocs/op |
| Journal read 100,000 events | 234.818 ms/op | 89.24 MB/s; 287,926,362 B/op | 1,233,448 allocs/op |
| Journal reduce 100,000 events | 10.511 ms/op | 3,984 B/op | 20 allocs/op |
| Provider process startup | 1.397 ms/op | 111,580 B/op | 200 allocs/op |

The planning fixture excludes real hashing, subprocesses, and operation-DAG construction. Journal append uses no-op writes/syncs and measures CPU/allocation overhead rather than durable disk throughput. The 1,000-operation execution sample uses the durable journal path and is sensitive to hosted-runner filesystem performance.
