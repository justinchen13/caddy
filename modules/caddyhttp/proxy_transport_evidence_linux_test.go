//go:build linux

package caddyhttp

import (
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestProxyTransportInfoEveryScalarLengthAndZero(t *testing.T) {
	for _, f := range proxyTCPInfoFields {
		t.Run(f.name, func(t *testing.T) {
			info := new(unix.TCPInfo)
			out := proxyTCPUnavailable("test")
			proxyTCPDecodeInfo(out, info, uint32(f.offset+f.width-1), nil)
			if out[f.name] != nil {
				t.Fatalf("partial %s fabricated a value", f.name)
			}
			out = proxyTCPUnavailable("test")
			proxyTCPDecodeInfo(out, info, uint32(f.offset+f.width), nil)
			switch f.width {
			case 1:
				if out[f.name] != uint8(0) {
					t.Fatalf("zero/type lost: %T %v", out[f.name], out[f.name])
				}
			case 4:
				if out[f.name] != uint32(0) {
					t.Fatalf("zero/type lost: %T %v", out[f.name], out[f.name])
				}
			case 8:
				if out[f.name] != uint64(0) {
					t.Fatalf("zero/type lost: %T %v", out[f.name], out[f.name])
				}
			}
		})
	}
	info := &unix.TCPInfo{State: 1, Rtt: 159000, Bytes_sent: 123456789, Snd_wnd: 65535}
	for _, n := range []uint32{0, 1} {
		out := proxyTCPUnavailable("test")
		proxyTCPDecodeInfo(out, info, n, nil)
		if out["available"] != (n == 1) || out["rtt_us"] != nil || out["bytes_sent"] != nil || out["snd_wnd_bytes"] != nil {
			t.Fatalf("length %d exposed an absent tail: %+v", n, out)
		}
	}
	out := proxyTCPUnavailable("test")
	proxyTCPDecodeInfo(out, info, uint32(unsafe.Sizeof(*info)), nil)
	if out["state"] != uint8(1) || out["rtt_us"] != uint32(159000) || out["bytes_sent"] != uint64(123456789) || out["snd_wnd_bytes"] != uint32(65535) {
		t.Fatal("full snapshot changed old field types or values")
	}
	out = proxyTCPUnavailable("test")
	proxyTCPDecodeInfo(out, info, uint32(unsafe.Sizeof(*info)), syscall.EINVAL)
	if out["available"] != false || out["tcp_info_length"] != nil || out["state"] != nil {
		t.Fatal("failed syscall looked available")
	}
}

func TestProxyTransportPacingAndAppLimited(t *testing.T) {
	info := &unix.TCPInfo{Pacing_rate: ^uint64(0), Max_pacing_rate: 0}
	end := uint32(unsafe.Offsetof(info.Pacing_rate) + unsafe.Sizeof(info.Pacing_rate))
	out := proxyTCPUnavailable("test")
	proxyTCPDecodeInfo(out, info, end-1, nil)
	if out["pacing_rate_Bps"] != nil || out["pacing_rate_unlimited"] != nil {
		t.Fatal("short pacing invented a value")
	}
	out = proxyTCPUnavailable("test")
	proxyTCPDecodeInfo(out, info, uint32(unsafe.Sizeof(*info)), nil)
	if out["pacing_rate_Bps"] != nil || out["pacing_rate_unlimited"] != true || out["max_pacing_rate_Bps"] != uint64(0) || out["max_pacing_rate_unlimited"] != false {
		t.Fatal("unlimited and genuine zero are not distinguishable")
	}
	for _, tc := range []struct {
		flag         byte
		little, want bool
	}{
		{0, true, false}, {1, true, true}, {0x80, true, false},
		{0, false, false}, {0x80, false, true}, {1, false, false},
	} {
		if got := proxyTCPAppLimited(tc.flag, tc.little); got != tc.want {
			t.Fatalf("endian decode: %+v got %v", tc, got)
		}
	}
	// The syscall writes the unnamed byte in the pinned Go ABI directly.
	(*[8]byte)(unsafe.Pointer(info))[7] = 0xff
	out = proxyTCPUnavailable("test")
	proxyTCPDecodeInfo(out, info, 7, nil)
	if out["delivery_rate_app_limited"] != nil {
		t.Fatal("byte7 was not returned")
	}
	out = proxyTCPUnavailable("test")
	proxyTCPDecodeInfo(out, info, 8, nil)
	if out["delivery_rate_app_limited"] != true {
		t.Fatal("native byte7 flag missing")
	}
}

func TestProxyTransportAlgorithmAndBBRBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		size uint32
		err  error
		want string
	}{
		{"bbr\x00", 4, nil, "bbr"}, {"cubic\x00", 6, nil, "cubic"},
		{"private-module\x00", 15, nil, "unknown"}, {"bbr", 3, nil, ""},
		{"bbr\x00", 0, nil, ""}, {"bbr\x00", 17, nil, ""}, {"\x00", 1, nil, ""},
		{"bbr\x00", 4, syscall.ENOPROTOOPT, ""},
	} {
		var raw [16]byte
		copy(raw[:], tc.name)
		out := proxyTCPUnavailable("test")
		if got := proxyTCPDecodeAlgorithm(out, raw, tc.size, tc.err); got != tc.want {
			t.Fatalf("algorithm %q: %q", tc.name, got)
		}
		b, _ := json.Marshal(out)
		if strings.Contains(string(b), "private-module") {
			t.Fatal("unknown module name escaped allowlist")
		}
	}
	info := &unix.TCPBBRInfo{Bw_lo: 7, Bw_hi: 2, Min_rtt: 0, Pacing_gain: 320, Cwnd_gain: 512}
	for _, tc := range []struct {
		name string
		end  uint32
		want any
	}{
		{"bbr_bandwidth_Bps", 8, uint64(2)<<32 | 7}, {"bbr_min_rtt_us", 12, uint32(0)},
		{"bbr_pacing_gain_q8", 16, uint32(320)}, {"bbr_cwnd_gain_q8", 20, uint32(512)},
	} {
		out := proxyTCPUnavailable("test")
		proxyTCPDecodeBBR(out, info, tc.end-1, nil)
		if out[tc.name] != nil {
			t.Fatalf("partial BBR scalar %s exposed", tc.name)
		}
		out = proxyTCPUnavailable("test")
		proxyTCPDecodeBBR(out, info, tc.end, nil)
		if out[tc.name] != tc.want {
			t.Fatalf("BBR %s = %v want %v", tc.name, out[tc.name], tc.want)
		}
	}
	for _, tc := range []struct {
		size uint32
		err  error
	}{{0, nil}, {7, nil}, {20, syscall.EINVAL}} {
		out := proxyTCPUnavailable("test")
		proxyTCPDecodeBBR(out, info, tc.size, tc.err)
		if out["cc_info_available"] != false || out["bbr_bandwidth_Bps"] != nil {
			t.Fatal("empty/failed/partial BBR looked valid")
		}
	}
}

type proxyTransportRaw struct {
	err   error
	calls int
}

func (r *proxyTransportRaw) Control(f func(uintptr)) error {
	r.calls++
	if r.err == nil {
		f(42)
	}
	return r.err
}
func (*proxyTransportRaw) Read(func(uintptr) bool) error  { return errors.New("unexpected Read") }
func (*proxyTransportRaw) Write(func(uintptr) bool) error { return errors.New("unexpected Write") }

type proxyTransportConn struct {
	net.Conn
	raw     syscall.RawConn
	err     error
	queries atomic.Int32
}

func (c *proxyTransportConn) SyscallConn() (syscall.RawConn, error) {
	c.queries.Add(1)
	return c.raw, c.err
}

func TestProxyTransportGroupsIndependentFailureAndNoBBRGuess(t *testing.T) {
	for _, algorithm := range []string{"bbr", "cubic", "unknown"} {
		t.Run(algorithm, func(t *testing.T) {
			raw := new(proxyTransportRaw)
			conn := &proxyTransportConn{raw: raw}
			bbrCalls := 0
			ops := proxyTCPSnapshotOps{
				info: func(fd int) (*unix.TCPInfo, uint32, error) {
					if fd != 42 {
						t.Fatal("fd changed")
					}
					return nil, 200, syscall.EINVAL
				},
				algorithm: func(fd int) ([16]byte, uint32, error) {
					var a [16]byte
					copy(a[:], algorithm)
					return a, uint32(len(algorithm) + 1), nil
				},
				bbr:       func(fd int) (*unix.TCPBBRInfo, uint32, error) { bbrCalls++; return nil, 20, syscall.ENOPROTOOPT },
				sendQueue: func(int) (int, error) { return 0, nil }, receiveQueue: func(int) (int, error) { return 0, syscall.EINVAL },
			}
			out := proxyTCPSnapshotWithOps(conn, ops)
			wantCalls := 0
			if algorithm == "bbr" {
				wantCalls = 1
			}
			if raw.calls != 1 || bbrCalls != wantCalls || out["available"] != false || out["congestion_algorithm"] != algorithm || out["cc_info_available"] != false || out["bbr_bandwidth_Bps"] != nil || out["send_queue_bytes"] != 0 || out["receive_queue_bytes"] != nil {
				t.Fatalf("failure groups conflated: %+v", out)
			}
			for _, group := range []string{"sample", "tcp_info_sample", "congestion_sample", "queue_sample"} {
				if out[group+"_started_unix_ms"] == nil || out[group+"_completed_unix_ms"] == nil || out[group+"_duration_us"].(int64) < 0 {
					t.Fatal("missing independent timing", group)
				}
			}
			if algorithm != "bbr" && out["cc_info_sample_started_unix_ms"] != nil {
				t.Fatal("unattempted BBR has a fabricated capture")
			}
		})
	}
	for _, conn := range []*proxyTransportConn{{raw: &proxyTransportRaw{err: syscall.EBADF}}, {err: syscall.EBADF}} {
		out := proxyTCPSnapshotWithOps(conn, proxyTCPSnapshotOps{})
		if out["available"] != false || out["state"] != nil || out["congestion_algorithm"] != nil || out["bbr_bandwidth_Bps"] != nil {
			t.Fatal("closed/unavailable fd produced counters")
		}
	}
}

func TestProxyTransportRealSocketCachePrivacyAndClose(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	client, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	p, _ := testEvidence(server)
	first, _ := p.tcpSample(false)
	if first["available"] != true || first["congestion_available"] != true || first["transport_evidence_version"] != 2 || first["sample_atomic"] != false {
		t.Fatalf("real socket groups unavailable: %+v", first)
	}
	serialized, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "127.0.0.1") || strings.Contains(string(serialized), server.RemoteAddr().String()) {
		t.Fatal("address in evidence")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				cached, _ := p.tcpSample(false)
				if !reflect.DeepEqual(first, cached) {
					t.Error("immutable cache changed")
				}
				if _, err := json.Marshal(cached); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if p.tcpSequence != 1 {
		t.Fatal("cache caused extra samples")
	}
	server.Close()
	closed := proxyTCPSnapshot(server)
	if closed["available"] != false || closed["state"] != nil || closed["congestion_algorithm"] != nil || closed["bbr_bandwidth_Bps"] != nil {
		t.Fatal("closed fd reused live values")
	}
	p.closed.Store(true)
	closed, _ = p.tcpSample(false)
	if closed["unavailable_reason"] != "connection_closed" || closed["state"] != nil {
		t.Fatal("closed wrapper reused cache")
	}
}

func TestProxyTransportInactivePathHasNoQueries(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	conn := &proxyTransportConn{Conn: a, err: syscall.EBADF}
	p, logs := testEvidence(conn)
	reader := make(chan struct{})
	go func() { defer close(reader); b.Read(make([]byte, 1)) }()
	if _, err := p.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	<-reader
	p.Close()
	if conn.queries.Load() != 0 || logs.Len() != 0 || p.tcpSequence != 0 {
		t.Fatal("inactive wrapper added diagnostic syscalls/logs")
	}
	// No test sets TCP_CONGESTION, deadlines or impairment on real TCP sockets.
}
