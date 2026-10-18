//go:build linux

package caddyhttp

import (
	"net"
	"testing"

	"golang.org/x/sys/unix"
)

var proxyTransportBenchmarkSink map[string]any

// Copy this file unchanged to the exact baseline. Run each ABBA arm in its own
// process with -benchtime=1000x. CPU is process user+system usage; ns/op is only
// wall time. Setup and teardown are outside both timed regions. This exercises
// the uncached syscall path; the unchanged production cache limits it to 1Hz.
func BenchmarkProxyTransportSnapshot(b *testing.B) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer l.Close()
	client, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close()
	server, err := l.Accept()
	if err != nil {
		b.Fatal(err)
	}
	defer server.Close()
	if proxyTCPSnapshot(server)["available"] != true {
		b.Fatal("real socket unavailable")
	}
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
	cpu := float64(after.Utime.Sec-before.Utime.Sec+after.Stime.Sec-before.Stime.Sec)*1e6 +
		float64(after.Utime.Usec-before.Utime.Usec+after.Stime.Usec-before.Stime.Usec)
	b.ReportMetric(cpu/float64(b.N), "cpu-us/op")
}
