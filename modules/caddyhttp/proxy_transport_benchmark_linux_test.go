//go:build linux

package caddyhttp

import (
	"encoding/json"
	"net"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

var proxyTransportBenchmarkSink map[string]any
var proxyTransportBenchmarkJSONSink []byte

// Copy this file unchanged to the exact baseline. Run each ABBA arm in its own
// process with -benchtime=1000x. CPU is process user+system usage; ns/op is only
// wall time. Setup and teardown are outside both timed regions. This exercises
// the uncached syscall path; the unchanged production cache limits it to 1Hz.
func BenchmarkProxyTransportSnapshot(b *testing.B) {
	server, _ := proxyTransportBenchmarkSocket(b)
	var before, after unix.Rusage
	b.ReportAllocs()
	b.ResetTimer()
	if err := unix.Getrusage(unix.RUSAGE_SELF, &before); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		proxyTransportBenchmarkSink = proxyTCPSnapshot(server)
	}
	if err := unix.Getrusage(unix.RUSAGE_SELF, &after); err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	proxyTransportBenchmarkCPU(b, before, after)
}

// Separately measures JSON encoding of a real snapshot. This does not include
// snapshot syscalls, the whole log event, sink I/O or proxy end-to-end latency.
// It is reported separately from the predeclared snapshot acceptance gate.
func BenchmarkProxyTransportSnapshotJSON(b *testing.B) {
	_, snapshot := proxyTransportBenchmarkSocket(b)
	var before, after unix.Rusage
	b.ReportAllocs()
	b.ResetTimer()
	if err := unix.Getrusage(unix.RUSAGE_SELF, &before); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		var err error
		proxyTransportBenchmarkJSONSink, err = json.Marshal(snapshot)
		if err != nil {
			b.Fatal(err)
		}
	}
	if err := unix.Getrusage(unix.RUSAGE_SELF, &after); err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	proxyTransportBenchmarkCPU(b, before, after)
	b.ReportMetric(float64(len(proxyTransportBenchmarkJSONSink)), "encoded-B/op")
}

func proxyTransportBenchmarkCPU(b *testing.B, before, after unix.Rusage) {
	cpu := float64(after.Utime.Sec-before.Utime.Sec+after.Stime.Sec-before.Stime.Sec)*1e6 +
		float64(after.Utime.Usec-before.Utime.Usec+after.Stime.Usec-before.Stime.Usec)
	b.ReportMetric(cpu/float64(b.N), "cpu-us/op")
}

func proxyTransportBenchmarkSocket(b *testing.B) (net.Conn, map[string]any) {
	b.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { l.Close() })
	client, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { client.Close() })
	server, err := l.Accept()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { server.Close() })
	raw, err := server.(syscall.Conn).SyscallConn()
	if err != nil {
		b.Fatal(err)
	}
	var algorithm string
	var optionErr error
	if err := raw.Control(func(fd uintptr) {
		algorithm, optionErr = unix.GetsockoptString(int(fd), unix.IPPROTO_TCP, unix.TCP_CONGESTION)
	}); err != nil {
		b.Fatal(err)
	}
	if optionErr != nil {
		b.Fatal(optionErr)
	}
	if algorithm != "bbr" {
		b.Fatalf("BBR cost fixture requires existing bbr socket, got %q; no socket settings changed", algorithm)
	}
	snapshot := proxyTCPSnapshot(server)
	if snapshot["available"] != true {
		b.Fatal("real socket unavailable")
	}
	// The exact baseline does not have the version or CC_INFO fields. Keep this
	// fixture source-identical while requiring the candidate's BBR read to work.
	if snapshot["transport_evidence_version"] == 2 && snapshot["cc_info_available"] != true {
		b.Fatal("v2 BBR CC_INFO path unavailable")
	}
	b.Logf("actual algorithm=%s; evidence_version=%v; cc_info_available=%v", algorithm,
		snapshot["transport_evidence_version"], snapshot["cc_info_available"])
	return server, snapshot
}
