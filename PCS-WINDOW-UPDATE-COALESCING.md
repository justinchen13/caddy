# Body-read WINDOW_UPDATE coalescing experiment

This candidate starts from production Caddy `97690a054a3f5fab673057c469eda35035ce0e43`.
Its only runtime change selects the independent Net experiment
`58d5eddc71f87b2c9b4890de33b8e062402befcf` when
`PCS_PROXY_COALESCE_WINDOW_UPDATES=1`. The default is off. Forwardproxy remains
`b1e4e257f5f3ca9774cb10744f86684e532f0d2a`.

The Net change queues connection and stream WINDOW_UPDATE frames from one
body-read notification before scheduling, without changing credit thresholds,
adding a timer, or altering authentication, TLS, or padding. A single lower
write is not a guarantee of one TLS record or TCP packet.

This candidate does not include the separate slow-write feedback experiment.
It is not production approved. Remote contract/race/regression results, an old
implementation negative control, and frozen network/resource comparisons are
required before any release decision. Old production versus new OFF must be
measured separately from new OFF versus ON. All upload timings stay fixed in
the source experiment; earlier delayed-upload results were mechanism evidence,
not qualified performance acceptance.
