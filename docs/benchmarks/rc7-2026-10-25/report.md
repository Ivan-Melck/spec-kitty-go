# Spec Kitty RC7: Go vs Python benchmarks

On this Linux host, the Go CLI was **12.3–16.8× faster on the tested project operations**,
**126–145× faster for version/help startup**, and used **58–76% less peak memory**.
The comparison covers 410 measured invocations across ten workloads on 2026-10-25.

These results measure local CLI performance. Full AI mission throughput also depends
on model and tool latency.

## Elapsed time

Values are milliseconds. Speedup is Python median divided by Go median.

| Operation | Python median | Go median | Go speedup | Python p95 | Go p95 |
| --- | ---: | ---: | ---: | ---: | ---: |
| Version (`--version`) | 1,170.7 | 8.1 | 144.5× | 1,201.4 | 8.8 |
| Help (`--help`) | 1,213.9 | 9.6 | 126.4× | 1,238.4 | 10.5 |
| Agent config list | 2,191.1 | 132.8 | 16.5× | 2,213.0 | 135.9 |
| Verify setup | 1,567.7 | 126.1 | 12.4× | 1,595.9 | 131.4 |
| Charter status (no active charter) | 1,443.4 | 113.8 | 12.7× | 1,456.1 | 116.5 |
| Task status (2 work packages) | 1,686.1 | 118.9 | 14.2× | 1,718.7 | 123.4 |
| Next query (missing-analysis guard) | 767.6 | 12.5 | 61.2× | 780.2 | 13.5 |
| Initialize project (existing home) | 3,165.7 | 245.1 | 12.9× | 3,241.3 | 255.4 |
| Initialize project (fresh home) | 6,108.5 | 364.6 | 16.8× | 6,286.7 | 375.2 |
| Create mission | 1,911.8 | 155.6 | 12.3× | 1,936.1 | 157.9 |

Version/help and the guarded `next` query skip much of project startup. Their
speedups do not predict implementation dispatch or full mission throughput.

## CPU time and memory

CPU time is the median user + system time in milliseconds. Memory is the median
per-invocation peak resident set size (RSS) in MiB, measured with Linux `wait4`.
Resource accounting includes reaped descendants; RSS is not simultaneous memory
use across the entire process tree.

| Operation | Python CPU (ms) | Go CPU (ms) | Python peak RSS (MiB) | Go peak RSS (MiB) |
| --- | ---: | ---: | ---: | ---: |
| Version (`--version`) | 1,164.3 | 11.7 | 110.7 | 26.8 |
| Help (`--help`) | 1,207.3 | 14.8 | 113.1 | 27.3 |
| Agent config list | 2,177.5 | 167.7 | 107.5 | 35.4 |
| Verify setup | 1,555.2 | 151.9 | 83.7 | 35.0 |
| Charter status (no active charter) | 1,436.6 | 141.1 | 87.6 | 34.9 |
| Task status (2 work packages) | 1,673.7 | 144.4 | 103.3 | 35.0 |
| Next query (missing-analysis guard) | 757.5 | 17.8 | 86.3 | 30.6 |
| Initialize project (existing home) | 3,099.5 | 323.4 | 120.6 | 37.6 |
| Initialize project (fresh home) | 5,984.9 | 457.7 | 124.6 | 39.4 |
| Create mission | 1,873.1 | 165.3 | 103.1 | 34.1 |

## Installation footprint

Logical file sizes, with hardlinks counted once:

| Component | Size (MiB) |
| --- | ---: |
| Go standalone executable | 39.9 |
| Python virtual environment (CLI and dependencies) | 92.7 |
| Python shared interpreter (additional to the virtual environment) | 102.7 |
| Go generated home assets | 4.1 |
| Python generated home assets | 4.1 |

The Python interpreter can be shared with other environments. Generated home
assets are listed separately from installation sizes. Build/install time was not measured.

## Environment and revisions

- CPU: AMD Ryzen 7 5800X 8-Core Processor (16 logical CPUs).
- Platform: `Linux-7.2.11-arch1-1-x86_64-with-glibc2.44`.
- Go toolchain: `go1.27.3-X:nodwarf5`; `CGO_ENABLED=0 go build -trimpath`.
- Python runtime: Python 3.13.15.
- Go source: `770e770fb35588200bb65ac17052eb597012d2ca`.
- Upstream Python RC7: `1b0fe9fc388b095f9964471b0177b4213e9c6c98` (`v4.0.0rc7`).
- Both executables reported `spec-kitty-cli version 4.0.0rc7`.
- Go executable SHA-256: `d992bc8292fd68cfe24bb59930f9dc193efcb8cf24aef16e18704f52bbed0897`.
- Installed Python package versions: [python-requirements.txt](python-requirements.txt).

## Method and validation

- 25 measured runs per executable for each of seven read commands; 10 for each of three initialization/creation workloads: 410 measured invocations total.
- Three additional warmup runs per executable per workload.
- Sequential pairs with randomized executable order and seed `20261025`.
- Warm operating-system caches; no shell or package-launcher overhead. A small C launcher measures fork/exec through `wait4` and avoids inheriting the Python driver’s RSS floor.
- Isolated application homes and project copies. Fixture copying happens outside the timed interval. Each initialization/creation run starts with a fresh project copy.
- A fresh Codex project without an active charter; mission queries use the repository’s finalized two-work-package fixture.
- Fresh-home initialization includes application asset installation. It does not drop operating-system caches.
- The `next` query exercises the expected `ANALYSIS_REPORT_MISSING` guard; it does not dispatch implementation.
- All timed commands exited successfully. Structured responses and created artifacts were checked.
- Task-status JSON matched exactly between implementations. Next-query JSON matched after excluding timestamps.

Measurements ran on a shared desktop host without CPU pinning. They cover local
CLI operations, with no remote/network workflows or large-project scaling tests.
For ten-sample workloads, the nearest-rank p95 is the maximum observed sample.

## Reproduce

Requires Linux, Python 3, a C compiler, Git, Go, and an installed upstream RC7
reference virtual environment. Run from the repository root:

```sh
CGO_ENABLED=0 go build -trimpath -o /var/tmp/spec-kitty-benchmark ./cmd/spec-kitty
python3 scripts/benchmark-rc7.py \
  --go-bin /var/tmp/spec-kitty-benchmark \
  --python-bin "$HOME/.cache/spec-kitty-parity/rc7-1b0fe9fc38/oracle-bin/spec-kitty" \
  --output /var/tmp/spec-kitty-benchmark-results
```

The [benchmark driver](../../../scripts/benchmark-rc7.py) and
[resource-measurement launcher](../../../scripts/benchmark-exec.c) contain the
fixture setup, workload arguments, timing logic, and summary calculations.

## Supporting artifacts

- [Raw measurements and metadata](results.json), including every sample, medians, means, standard deviations, minima, maxima, and p95 timings.
- [Captured command output](command-output/) from each executable and workload.
- [Interactive HTML report](report.html): download and open locally to switch between elapsed time, CPU time, and peak memory.
