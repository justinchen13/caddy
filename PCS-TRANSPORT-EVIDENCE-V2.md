# Transport evidence v2 — candidate, not a performance fix

This candidate starts from production source `deb690` and changes observation only.
It does not include the DATA quantum experiment or change socket settings,
congestion control, authentication, TLS, padding, HTTP/2 flow control or scheduling.
Compilation, functional/race validation, cost acceptance and production coverage
remain release gates; this document is not evidence that those gates passed.

## Capture contract

Every snapshot, including unsupported and closed sockets, carries
`transport_evidence_version: 2`. All transport scalars start as null. A successful
TCP_INFO call only publishes a scalar when the returned optlen includes its entire
offset and width in the pinned x/sys ABI. A genuine zero remains numeric zero.
`available: true` means at least the first TCP_INFO field was returned, not that
all fields exist. Failed calls do not publish the caller-supplied buffer capacity
as a returned length. Existing scalar types are preserved.

Sampling reads the accepted socket's original fd. It performs no setsockopt,
address lookup, socket duplication or payload capture. The existing per-connection
one-second cache and one final pre-close refresh are unchanged. No new sampling
runs on the disabled/inactive path. Published cached maps are immutable.

The overall snapshot and its TCP_INFO, TCP_CONGESTION, TCP_CC_INFO and queue-ioctl
groups record separate start/completion Unix milliseconds and monotonic duration
microseconds. These sequential reads are explicitly `sample_atomic: false`.
Unattempted groups have null timing. Application-state timestamps and TCP timing
must be joined with an explicit age limit, not treated as a simultaneous sample.
Reuse the original sample sequence; never sum repeated cached lifetime counters.
Unknown, failed, truncated and cross-epoch samples are comparison barriers.

## Interpretation

* CA state, RTO, retransmits/backoff/probes, retransmitted and SACKed segments,
  segment totals, DSACK and delivery counters help distinguish recovery and
  progress. They are kernel counters/gauges, not a user failure rate.
* RTT, RTO, minimum RTT and busy/limited times are microseconds. Last-data/ACK
  ages are milliseconds. MSS, PMTU, window, queue and byte counters are bytes.
* Delivery and pacing rates are bytes per second (`Bps`); they are kernel
  estimates/settings, not measured application throughput. `UINT64_MAX` pacing
  becomes a null rate and a separate true unlimited flag. Numeric zero and
  unlimited remain distinguishable.
* The application-limited bit is read from returned TCP_INFO byte 7, using native
  little-/big-endian C bitfield order, only with the expected ABI layout. Missing
  bytes and unsupported layouts remain unknown. Both endian masks are tested;
  actual big-endian hardware is not exercised by the local fixture.
* TCP_CONGESTION is a fixed 16-byte read with a fixed algorithm allowlist. Unknown
  module names export only `unknown`; no endpoint, target or user is added.
* Only a positively identified `bbr` socket gets TCP_CC_INFO. Its returned length
  independently gates bandwidth (`Bw_hi << 32 | Bw_lo`, bytes/s), minimum RTT and
  pacing/cwnd gains (raw Q8 values, divide by 256 to interpret). No BBR mode is
  inferred. Other algorithms, syscall failure or partial fields stay explicit.
* Short optlen tests inject old-kernel responses. Real loopback tests use the
  host's existing algorithm without selecting BBR or changing network options.

## Reproducible acceptance

New tests are in `proxy_transport_evidence_linux_test.go`; existing TestProxy
regressions remain unchanged. Relevant focused commands, run by the coordinated
builder, are:

```sh
go test ./modules/caddyhttp -run TestProxy -count=1
go test -race ./modules/caddyhttp -run 'TestProxyTransport|TestProxyEvidenceTCP' -count=1
go test ./modules/caddyhttp -run '^$' -bench '^BenchmarkProxyTransportSnapshot(JSON)?$' -benchtime=1000x -count=1
```

Copy `proxy_transport_benchmark_linux_test.go` unchanged to the exact baseline.
Run independent processes in baseline/candidate/candidate/baseline order. The
benchmark calls only the uncached real-socket snapshot path, uses Getrusage process
user+system CPU delta to report `cpu-us/op`, and reports allocations. Wall `ns/op`
is not substituted for CPU. Setup and teardown are outside measurement. Record
the actual socket algorithm and CC_INFO availability alongside each arm. The
fixture uses read-only getsockopt and fails if the actual algorithm is not BBR;
v2 additionally requires successful CC_INFO. It does not change socket options.
A separate `BenchmarkProxyTransportSnapshotJSON` reports encoding CPU, allocation
and serialized size for the real snapshot, without applying the snapshot cost
gate to this separate metric. Neither benchmark measures the complete log event,
logger/exporter I/O or end-to-end proxy tail latency; those need separate runtime
coverage/cost review.
Predeclared additional cost limits are 100 CPU microseconds and 16 KiB allocated
per sample; production retains the 1 Hz cache. Loopback observation cost is not a
public-network speed improvement. Production coverage and sample duration must
also be checked after any eventual release.
