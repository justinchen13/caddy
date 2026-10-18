package caddyhttp

import (
	"sync"
	"time"
)

// Short critical sections only copy scalar metadata. They never cover the
// underlying Read or logging/syscalls. Concurrent Reads are legal; if they
// overlap, an exact oldest pending age is unknown until the overlap drains.
type proxyReadState struct {
	mu                 sync.Mutex
	entered, completed uint64
	inFlight           int
	started, returned  time.Time
	ambiguous          bool
	lastBytes          int
	lastError          string
}

func (r *proxyReadState) begin() {
	r.mu.Lock()
	r.entered++
	if r.inFlight == 0 {
		r.started = time.Now()
		r.ambiguous = false
	} else {
		r.ambiguous = true
	}
	r.inFlight++
	r.mu.Unlock()
}

func (r *proxyReadState) complete(n int, class string) time.Time {
	r.mu.Lock()
	r.completed++
	r.inFlight--
	now := time.Now()
	r.returned = now
	r.lastBytes, r.lastError = n, class
	if r.inFlight == 0 {
		r.started = time.Time{}
		r.ambiguous = false
	}
	r.mu.Unlock()
	return now
}

func (r *proxyReadState) snapshot() map[string]any {
	r.mu.Lock()
	now := time.Now()
	entered, completed, inFlight := r.entered, r.completed, r.inFlight
	started, returned, ambiguous := r.started, r.returned, r.ambiguous
	lastBytes, lastError := r.lastBytes, r.lastError
	r.mu.Unlock()
	out := map[string]any{"unix_ms": now.UnixMilli(), "calls_entered": entered,
		"calls_completed": completed, "in_flight": inFlight,
		"pending_age_ms": nil, "pending_age_ambiguous": ambiguous,
		"last_return_age_ms": nil, "last_return_bytes": nil, "last_return_error_class": nil}
	if inFlight == 1 && !ambiguous && !started.IsZero() {
		out["pending_age_ms"] = float64(now.Sub(started)) / float64(time.Millisecond)
	}
	if !returned.IsZero() {
		out["last_return_age_ms"] = float64(now.Sub(returned)) / float64(time.Millisecond)
		out["last_return_bytes"], out["last_return_error_class"] = lastBytes, lastError
	}
	return out
}

// All exported transport scalars exist on every platform. A missing/short/
// failed source is null, while an observed kernel zero keeps its numeric type.
var proxyTCPNullableFields = [...]string{
	"state",
	"ca_state",
	"retransmits",
	"probes",
	"backoff",
	"rto_us",
	"snd_mss",
	"rcv_mss",
	"unacked_segments",
	"sacked_segments",
	"lost_segments",
	"retrans_out",
	"last_data_sent_ms",
	"last_data_recv_ms",
	"last_ack_recv_ms",
	"pmtu",
	"rtt_us",
	"rttvar_us",
	"snd_cwnd",
	"total_retrans_segments",
	"bytes_acked",
	"bytes_received",
	"segs_out",
	"segs_in",
	"notsent_bytes",
	"min_rtt_us",
	"data_segs_in",
	"data_segs_out",
	"delivery_rate_Bps",
	"busy_time_us",
	"rwnd_limited_us",
	"sndbuf_limited_us",
	"delivered",
	"delivered_ce",
	"bytes_sent",
	"bytes_retrans",
	"dsack_dups",
	"snd_wnd_bytes",
	"send_queue_bytes",
	"receive_queue_bytes",
	"tcp_info_length",
	"tcp_congestion_length",
	"tcp_cc_info_length",
	"pacing_rate_Bps",
	"pacing_rate_unlimited",
	"max_pacing_rate_Bps",
	"max_pacing_rate_unlimited",
	"delivery_rate_app_limited",
	"delivery_rate_app_limited_status",
	"congestion_algorithm",
	"bbr_bandwidth_Bps",
	"bbr_min_rtt_us",
	"bbr_pacing_gain_q8",
	"bbr_cwnd_gain_q8",
}

func proxyTCPUnavailable(reason string) map[string]any {
	now := time.Now().UnixMilli()
	out := make(map[string]any, len(proxyTCPNullableFields)+28)
	for _, name := range proxyTCPNullableFields {
		out[name] = nil
	}
	out["transport_evidence_version"] = 2
	out["sample_atomic"] = false
	out["available"], out["unavailable_reason"] = false, reason
	out["congestion_available"], out["congestion_unavailable_reason"] = false, reason
	out["cc_info_available"], out["cc_info_unavailable_reason"] = false, reason
	out["sample_started_unix_ms"], out["sample_completed_unix_ms"] = now, now
	out["sample_duration_us"], out["sample_sequence"] = int64(0), nil
	for _, group := range [...]string{"tcp_info_sample", "congestion_sample", "cc_info_sample", "queue_sample"} {
		out[group+"_started_unix_ms"] = nil
		out[group+"_completed_unix_ms"] = nil
		out[group+"_duration_us"] = nil
	}
	return out
}

// Existing event logs can burst at 12/s. Keep all TCP syscalls at <=1 sample/s
// per socket, except for one final pre-close refresh. The cached map is immutable
// after publication. A closed wrapper never reuses its cache for later events.
func (p *ProxyEvidence) tcpSample(final bool) (map[string]any, float64) {
	p.tcpMu.Lock()
	defer p.tcpMu.Unlock()
	if p.closed.Load() && !final {
		return proxyTCPUnavailable("connection_closed"), 0
	}
	if final || p.tcpCached == nil || time.Since(p.tcpCaptured) >= time.Second {
		p.tcpCached = proxyTCPSnapshot(p.Conn)
		p.tcpSequence++
		p.tcpCached["sample_sequence"] = p.tcpSequence
		p.tcpCaptured = time.Now()
	}
	return p.tcpCached, float64(time.Since(p.tcpCaptured)) / float64(time.Millisecond)
}
