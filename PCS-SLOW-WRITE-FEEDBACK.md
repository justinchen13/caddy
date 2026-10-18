# Experimental completed-write feedback integration

This candidate adds one opt-in constructor selection to Caddy's existing HTTP/2
configuration. It requires the independently maintained net candidate `7036bdb`
(or the same tested runtime tree). It is not deployed and has no accepted
performance result.

`PCS_PROXY_LATENCY=1` retains the existing proxy scheduler. Only when
`PCS_PROXY_SLOW_WRITE_FEEDBACK=1` is also set does Caddy select
`NewProxyWriteSchedulerWithSlowWriteFeedback`. With either switch missing or not
exactly `1`, this new policy is inactive. Configuration is read at server setup;
changing a running process's external environment is not a live toggle.

The net policy temporarily limits a DATA scheduling turn to 4096 bytes after a
successful DATA or flush writer call takes at least 100 ms. Its two-second window
uses actual writer completion time, and requires at least two open streams.
Open streams do not establish concurrent active users. The policy cannot preempt
an already selected frame or remove bytes from TCP's queue. A slow writer call
does not identify the network, client, or scheduler as the underlying cause.

Authentication, TLS settings, padding, HTTP/2 flow limits, existing ping and
write timeouts, and evidence switches retain their prior configuration. The only
runtime edit in this Caddy candidate is constructor selection; the net candidate
adds timing fields and branches even when its policy is off, so default-off
overhead needs independent measurement.

Acceptance is still pending: full runtime build and integration, security and
protocol regression, old production versus new policy-off overhead, then the
same new binary with policy off versus on under a previously qualified private
network fixture. Existing real-TLS causal tests use an injected clock and prove
receive ordering only. They do not establish public latency or throughput gains.
The original failed or inconclusive experiments remain part of the record.
