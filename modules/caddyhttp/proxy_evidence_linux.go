//go:build linux

package caddyhttp

import (
	"bytes"
	"math"
	"net"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// All reads use the accepted socket's original fd. No socket options are set.
// Ops are passed by value so failure tests never mutate a global syscall hook.
type proxyTCPSnapshotOps struct {
	info         func(int) (*unix.TCPInfo, uint32, error)
	algorithm    func(int) ([16]byte, uint32, error)
	bbr          func(int) (*unix.TCPBBRInfo, uint32, error)
	sendQueue    func(int) (int, error)
	receiveQueue func(int) (int, error)
}

func proxyTCPSnapshot(conn net.Conn) map[string]any {
	return proxyTCPSnapshotWithOps(conn, proxyTCPSnapshotOps{
		info: proxyTCPInfo, algorithm: proxyTCPCongestion, bbr: proxyTCPBBRInfo,
		sendQueue:    func(fd int) (int, error) { return unix.IoctlGetInt(fd, unix.TIOCOUTQ) },
		receiveQueue: func(fd int) (int, error) { return unix.IoctlGetInt(fd, unix.TIOCINQ) },
	})
}

func proxyTCPSnapshotWithOps(conn net.Conn, ops proxyTCPSnapshotOps) map[string]any {
	started := time.Now()
	out := proxyTCPUnavailable("not_syscall_conn")
	defer func() { proxyTCPSampleTiming(out, "sample", started) }()
	c, ok := conn.(syscall.Conn)
	if !ok {
		return out
	}
	raw, err := c.SyscallConn()
	if err != nil {
		out["unavailable_reason"] = "syscall_conn_unavailable"
		return out
	}
	err = raw.Control(func(fd uintptr) {
		start := time.Now()
		info, size, infoErr := ops.info(int(fd))
		proxyTCPSampleTiming(out, "tcp_info_sample", start)
		proxyTCPDecodeInfo(out, info, size, infoErr)

		start = time.Now()
		name, size, algorithmErr := ops.algorithm(int(fd))
		proxyTCPSampleTiming(out, "congestion_sample", start)
		algorithm := proxyTCPDecodeAlgorithm(out, name, size, algorithmErr)
		if algorithm == "bbr" {
			start = time.Now()
			bbr, size, bbrErr := ops.bbr(int(fd))
			proxyTCPSampleTiming(out, "cc_info_sample", start)
			proxyTCPDecodeBBR(out, bbr, size, bbrErr)
		} else if algorithm != "" {
			out["cc_info_unavailable_reason"] = "not_bbr"
		} else {
			out["cc_info_unavailable_reason"] = "congestion_unavailable"
		}

		start = time.Now()
		if q, err := ops.sendQueue(int(fd)); err == nil {
			out["send_queue_bytes"] = q
		}
		// TIOCINQ is Linux FIONREAD: unread raw TCP bytes, below TLS.
		if q, err := ops.receiveQueue(int(fd)); err == nil {
			out["receive_queue_bytes"] = q
		}
		proxyTCPSampleTiming(out, "queue_sample", start)
	})
	if err != nil {
		out = proxyTCPUnavailable("raw_control_unavailable")
	}
	return out
}

func proxyTCPSampleTiming(out map[string]any, prefix string, started time.Time) {
	completed := time.Now()
	out[prefix+"_started_unix_ms"] = started.UnixMilli()
	out[prefix+"_completed_unix_ms"] = completed.UnixMilli()
	out[prefix+"_duration_us"] = completed.Sub(started).Microseconds()
}

type proxyTCPField struct {
	name          string
	offset, width uintptr
}

// Offsets come from the pinned x/sys ABI. A kernel-short tail remains null;
// values are read only after checking the entire scalar, preserving old types.
var proxyTCPInfoFields = [...]proxyTCPField{
	{"state", unsafe.Offsetof(unix.TCPInfo{}.State), unsafe.Sizeof(unix.TCPInfo{}.State)},
	{"ca_state", unsafe.Offsetof(unix.TCPInfo{}.Ca_state), unsafe.Sizeof(unix.TCPInfo{}.Ca_state)},
	{"retransmits", unsafe.Offsetof(unix.TCPInfo{}.Retransmits), unsafe.Sizeof(unix.TCPInfo{}.Retransmits)},
	{"probes", unsafe.Offsetof(unix.TCPInfo{}.Probes), unsafe.Sizeof(unix.TCPInfo{}.Probes)},
	{"backoff", unsafe.Offsetof(unix.TCPInfo{}.Backoff), unsafe.Sizeof(unix.TCPInfo{}.Backoff)},
	{"rto_us", unsafe.Offsetof(unix.TCPInfo{}.Rto), unsafe.Sizeof(unix.TCPInfo{}.Rto)},
	{"snd_mss", unsafe.Offsetof(unix.TCPInfo{}.Snd_mss), unsafe.Sizeof(unix.TCPInfo{}.Snd_mss)},
	{"rcv_mss", unsafe.Offsetof(unix.TCPInfo{}.Rcv_mss), unsafe.Sizeof(unix.TCPInfo{}.Rcv_mss)},
	{"unacked_segments", unsafe.Offsetof(unix.TCPInfo{}.Unacked), unsafe.Sizeof(unix.TCPInfo{}.Unacked)},
	{"sacked_segments", unsafe.Offsetof(unix.TCPInfo{}.Sacked), unsafe.Sizeof(unix.TCPInfo{}.Sacked)},
	{"lost_segments", unsafe.Offsetof(unix.TCPInfo{}.Lost), unsafe.Sizeof(unix.TCPInfo{}.Lost)},
	{"retrans_out", unsafe.Offsetof(unix.TCPInfo{}.Retrans), unsafe.Sizeof(unix.TCPInfo{}.Retrans)},
	{"last_data_sent_ms", unsafe.Offsetof(unix.TCPInfo{}.Last_data_sent), unsafe.Sizeof(unix.TCPInfo{}.Last_data_sent)},
	{"last_data_recv_ms", unsafe.Offsetof(unix.TCPInfo{}.Last_data_recv), unsafe.Sizeof(unix.TCPInfo{}.Last_data_recv)},
	{"last_ack_recv_ms", unsafe.Offsetof(unix.TCPInfo{}.Last_ack_recv), unsafe.Sizeof(unix.TCPInfo{}.Last_ack_recv)},
	{"pmtu", unsafe.Offsetof(unix.TCPInfo{}.Pmtu), unsafe.Sizeof(unix.TCPInfo{}.Pmtu)},
	{"rtt_us", unsafe.Offsetof(unix.TCPInfo{}.Rtt), unsafe.Sizeof(unix.TCPInfo{}.Rtt)},
	{"rttvar_us", unsafe.Offsetof(unix.TCPInfo{}.Rttvar), unsafe.Sizeof(unix.TCPInfo{}.Rttvar)},
	{"snd_cwnd", unsafe.Offsetof(unix.TCPInfo{}.Snd_cwnd), unsafe.Sizeof(unix.TCPInfo{}.Snd_cwnd)},
	{"total_retrans_segments", unsafe.Offsetof(unix.TCPInfo{}.Total_retrans), unsafe.Sizeof(unix.TCPInfo{}.Total_retrans)},
	{"bytes_acked", unsafe.Offsetof(unix.TCPInfo{}.Bytes_acked), unsafe.Sizeof(unix.TCPInfo{}.Bytes_acked)},
	{"bytes_received", unsafe.Offsetof(unix.TCPInfo{}.Bytes_received), unsafe.Sizeof(unix.TCPInfo{}.Bytes_received)},
	{"segs_out", unsafe.Offsetof(unix.TCPInfo{}.Segs_out), unsafe.Sizeof(unix.TCPInfo{}.Segs_out)},
	{"segs_in", unsafe.Offsetof(unix.TCPInfo{}.Segs_in), unsafe.Sizeof(unix.TCPInfo{}.Segs_in)},
	{"notsent_bytes", unsafe.Offsetof(unix.TCPInfo{}.Notsent_bytes), unsafe.Sizeof(unix.TCPInfo{}.Notsent_bytes)},
	{"min_rtt_us", unsafe.Offsetof(unix.TCPInfo{}.Min_rtt), unsafe.Sizeof(unix.TCPInfo{}.Min_rtt)},
	{"data_segs_in", unsafe.Offsetof(unix.TCPInfo{}.Data_segs_in), unsafe.Sizeof(unix.TCPInfo{}.Data_segs_in)},
	{"data_segs_out", unsafe.Offsetof(unix.TCPInfo{}.Data_segs_out), unsafe.Sizeof(unix.TCPInfo{}.Data_segs_out)},
	{"delivery_rate_Bps", unsafe.Offsetof(unix.TCPInfo{}.Delivery_rate), unsafe.Sizeof(unix.TCPInfo{}.Delivery_rate)},
	{"busy_time_us", unsafe.Offsetof(unix.TCPInfo{}.Busy_time), unsafe.Sizeof(unix.TCPInfo{}.Busy_time)},
	{"rwnd_limited_us", unsafe.Offsetof(unix.TCPInfo{}.Rwnd_limited), unsafe.Sizeof(unix.TCPInfo{}.Rwnd_limited)},
	{"sndbuf_limited_us", unsafe.Offsetof(unix.TCPInfo{}.Sndbuf_limited), unsafe.Sizeof(unix.TCPInfo{}.Sndbuf_limited)},
	{"delivered", unsafe.Offsetof(unix.TCPInfo{}.Delivered), unsafe.Sizeof(unix.TCPInfo{}.Delivered)},
	{"delivered_ce", unsafe.Offsetof(unix.TCPInfo{}.Delivered_ce), unsafe.Sizeof(unix.TCPInfo{}.Delivered_ce)},
	{"bytes_sent", unsafe.Offsetof(unix.TCPInfo{}.Bytes_sent), unsafe.Sizeof(unix.TCPInfo{}.Bytes_sent)},
	{"bytes_retrans", unsafe.Offsetof(unix.TCPInfo{}.Bytes_retrans), unsafe.Sizeof(unix.TCPInfo{}.Bytes_retrans)},
	{"dsack_dups", unsafe.Offsetof(unix.TCPInfo{}.Dsack_dups), unsafe.Sizeof(unix.TCPInfo{}.Dsack_dups)},
	{"snd_wnd_bytes", unsafe.Offsetof(unix.TCPInfo{}.Snd_wnd), unsafe.Sizeof(unix.TCPInfo{}.Snd_wnd)},
}

func proxyTCPFieldFits(size uint32, offset, width uintptr) bool {
	return offset+width <= uintptr(size)
}

func proxyTCPDecodeInfo(out map[string]any, info *unix.TCPInfo, size uint32, err error) {
	if err != nil || info == nil {
		out["unavailable_reason"] = "tcp_info_unavailable"
		return
	}
	out["tcp_info_length"] = size
	if size == 0 {
		out["unavailable_reason"] = "tcp_info_empty"
		return
	}
	out["available"] = true // At least state is present; each other field can be null.
	out["unavailable_reason"] = nil
	for _, f := range proxyTCPInfoFields {
		if !proxyTCPFieldFits(size, f.offset, f.width) {
			continue
		}
		p := unsafe.Add(unsafe.Pointer(info), f.offset)
		switch f.width {
		case 1:
			out[f.name] = *(*uint8)(p)
		case 4:
			out[f.name] = *(*uint32)(p)
		case 8:
			out[f.name] = *(*uint64)(p)
		}
	}
	proxyTCPDecodePacing(out, "pacing_rate", info.Pacing_rate, size,
		unsafe.Offsetof(info.Pacing_rate), unsafe.Sizeof(info.Pacing_rate))
	proxyTCPDecodePacing(out, "max_pacing_rate", info.Max_pacing_rate, size,
		unsafe.Offsetof(info.Max_pacing_rate), unsafe.Sizeof(info.Max_pacing_rate))
	if size < 8 {
		out["delivery_rate_app_limited_status"] = "field_not_returned"
	} else if unsafe.Offsetof(info.Rto) != 8 {
		out["delivery_rate_app_limited_status"] = "unsupported_layout"
	} else {
		// Linux tcp_info byte7 starts with a one-bit C bitfield. Its first
		// declared bit is LSB on little-endian and MSB on big-endian Linux.
		flag := (*[8]byte)(unsafe.Pointer(info))[7]
		var native uint16 = 1
		little := *(*byte)(unsafe.Pointer(&native)) == 1
		out["delivery_rate_app_limited"] = proxyTCPAppLimited(flag, little)
		out["delivery_rate_app_limited_status"] = "available"
	}
}

func proxyTCPAppLimited(flag byte, little bool) bool {
	if little {
		return flag&1 != 0
	}
	return flag&0x80 != 0
}

func proxyTCPDecodePacing(out map[string]any, name string, value uint64, size uint32, offset, width uintptr) {
	if !proxyTCPFieldFits(size, offset, width) {
		return
	}
	unlimited := value == math.MaxUint64
	out[name+"_unlimited"] = unlimited
	if !unlimited {
		out[name+"_Bps"] = value
	}
}

func proxyTCPDecodeAlgorithm(out map[string]any, raw [16]byte, size uint32, err error) string {
	if err != nil {
		out["congestion_unavailable_reason"] = "getsockopt_failed"
		return ""
	}
	out["tcp_congestion_length"] = size
	if size == 0 || size > uint32(len(raw)) {
		out["congestion_unavailable_reason"] = "invalid_length"
		return ""
	}
	end := bytes.IndexByte(raw[:size], 0)
	if end < 0 {
		out["congestion_unavailable_reason"] = "unterminated_name"
		return ""
	}
	if end == 0 {
		out["congestion_unavailable_reason"] = "empty_name"
		return ""
	}
	name := string(raw[:end])
	// Never export an unrecognized kernel/module-supplied name.
	switch name {
	case "bbr", "cubic", "reno", "bic", "dctcp", "htcp", "hybla", "illinois",
		"lp", "nv", "scalable", "vegas", "veno", "westwood", "yeah":
	default:
		name = "unknown"
	}
	out["congestion_available"] = true
	out["congestion_unavailable_reason"] = nil
	out["congestion_algorithm"] = name
	return name
}

func proxyTCPDecodeBBR(out map[string]any, info *unix.TCPBBRInfo, size uint32, err error) {
	if err != nil || info == nil {
		out["cc_info_unavailable_reason"] = "getsockopt_failed"
		return
	}
	out["tcp_cc_info_length"] = size
	if size == 0 {
		out["cc_info_unavailable_reason"] = "empty_info"
		return
	}
	// A partial scalar never creates available-looking zero data.
	if proxyTCPFieldFits(size, unsafe.Offsetof(info.Bw_hi), unsafe.Sizeof(info.Bw_hi)) {
		out["bbr_bandwidth_Bps"] = uint64(info.Bw_hi)<<32 | uint64(info.Bw_lo)
		out["cc_info_available"] = true
	}
	for _, f := range []struct {
		name          string
		offset, width uintptr
		value         uint32
	}{
		{"bbr_min_rtt_us", unsafe.Offsetof(info.Min_rtt), unsafe.Sizeof(info.Min_rtt), info.Min_rtt},
		{"bbr_pacing_gain_q8", unsafe.Offsetof(info.Pacing_gain), unsafe.Sizeof(info.Pacing_gain), info.Pacing_gain},
		{"bbr_cwnd_gain_q8", unsafe.Offsetof(info.Cwnd_gain), unsafe.Sizeof(info.Cwnd_gain), info.Cwnd_gain},
	} {
		if proxyTCPFieldFits(size, f.offset, f.width) {
			out[f.name] = f.value
			out["cc_info_available"] = true
		}
	}
	if out["cc_info_available"] == true {
		out["cc_info_unavailable_reason"] = nil
	} else {
		out["cc_info_unavailable_reason"] = "incomplete_field"
	}
}

// Preserve returned optlen. Failed calls leave all values unknown even if the
// kernel leaves the caller's input capacity in the length argument.
func proxyTCPInfo(fd int) (*unix.TCPInfo, uint32, error) {
	var info unix.TCPInfo
	size := uint32(unsafe.Sizeof(info))
	_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), unix.IPPROTO_TCP,
		unix.TCP_INFO, uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&size)), 0)
	if errno != 0 {
		return nil, size, errno
	}
	return &info, size, nil
}

func proxyTCPCongestion(fd int) ([16]byte, uint32, error) {
	var name [16]byte
	size := uint32(len(name))
	_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), unix.IPPROTO_TCP,
		unix.TCP_CONGESTION, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&size)), 0)
	if errno != 0 {
		return name, size, errno
	}
	return name, size, nil
}

func proxyTCPBBRInfo(fd int) (*unix.TCPBBRInfo, uint32, error) {
	var info unix.TCPBBRInfo
	size := uint32(unsafe.Sizeof(info))
	_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), unix.IPPROTO_TCP,
		unix.TCP_CC_INFO, uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&size)), 0)
	if errno != 0 {
		return nil, size, errno
	}
	return &info, size, nil
}
