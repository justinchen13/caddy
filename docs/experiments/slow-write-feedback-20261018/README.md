# Opt-in slow-write feedback: correctness checkpoint (2026-10-18 UTC)

Runtime inputs: Caddy `15f772923625b4dedba3225d34a8952980dd30bd`, x/net runtime
`7036bdbdc5225c8bef6d644b892ae08d6e644860` (documentation HEAD `ce23d31f`),
forwardproxy `b1e4e257f5f3ca9774cb10744f86684e532f0d2a`. These results do not authorize
a performance claim or deployment. The production binary remains unchanged.

Caddy 15 normal and 15 race tests and forwardproxy 41 tests with switches off and
41 with switches on passed. The original combined 900-second job timed out during
final binary compilation; its failure is retained in `functional-build-timeout.json`.
A separate binary-only build reused the stopped, offline Go1.25.6 cache under
0.35 CPU, 512MiB memory and no extra swap. Its actual build took 185.742 seconds.
Temporary source trees were removed; the cached container's startup inode, bytes,
mode and mtime and original resource settings were restored. Existing mounted source
remained clean. Cgroup OOM and OOM-kill deltas were zero; memory.max was hit 6902 times,
so this is not evidence of no memory pressure.

Candidate: `v2.11.1-pcsproxy9-feedback-candidate`, 46,985,400 bytes, SHA256
`643318adadee477ecc30f8aaaff91b1738f8a3ba9ec6a91338f1d21c870f5aa5`. Build flags:
`CGO_ENABLED=0`, `-p 1 -mod=readonly -buildvcs=false -trimpath`,
`-tags=nobadger,nomysql,nopgx`, `-ldflags=-s -w` plus the custom version.

A separate private loopback namespace exercised the actual binary with trusted
synthetic CA, SNI, ALPN h2, valid authentication, 42 CONNECT tunnels, body checksums
and tunnel EOF per case. With latency enabled, feedback unset, `0` and `true` all
selected `proxy_round_robin`; only exact `1` selected `proxy_slow_write_feedback`.
With latency disabled and feedback `1`, the policy is `round_robin` on Go1.25.6.

The first test incorrectly expected an empty policy name for that last case: both
reviewers missed the `!go1.27` implementation of `clientPriorityDisabled`, which
returns true. The original failure is preserved; the corrected single case was
frozen and rerun successfully. No runtime code or performance threshold changed,
and the other four passing cases were not rerun. Both units and cgroups were
cleaned, synthetic secret files removed, host network configuration unchanged,
and OOM deltas zero.

These are correctness and configuration-selection results. They do not demonstrate
that real slow-write feedback was triggered, lower tail latency, higher throughput,
or natural-user stability. Old-vs-new-disabled cost, disabled-vs-enabled qualified
network ABBA, and production observation remain required. The separately retained
network baselines failed qualification and are not used to claim a gain.
