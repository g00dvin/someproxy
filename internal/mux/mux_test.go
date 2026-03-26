package mux

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
)

// dummyConn is a minimal ReadWriteCloser for testing.
type dummyConn struct{ bytes.Buffer }

func (d *dummyConn) Close() error               { return nil }
func (d *dummyConn) Read(p []byte) (int, error) { return 0, nil }

func TestSelectConn_LatencyThreshold(t *testing.T) {
	m := newTestMux(3)

	// Set latencies: conn0=10ms, conn1=20ms, conn2=100ms (>3x of 10ms=30ms)
	m.conns[0].stats.latency.Store(10_000_000)  // 10ms
	m.conns[1].stats.latency.Store(20_000_000)  // 20ms
	m.conns[2].stats.latency.Store(100_000_000) // 100ms

	// Run selectConn 100 times, conn2 should never be selected
	counts := map[*muxConn]int{}
	for i := 0; i < 100; i++ {
		mc := m.selectConn()
		counts[mc]++
	}
	if counts[m.conns[2]] > 0 {
		t.Fatalf("conn2 (100ms) should be excluded, got %d selections", counts[m.conns[2]])
	}
	if counts[m.conns[0]] == 0 || counts[m.conns[1]] == 0 {
		t.Fatal("conn0 and conn1 should both be selected")
	}
}

func TestSelectConn_AllZeroLatency(t *testing.T) {
	m := newTestMux(3)
	// Should not exclude all — fallback to all conns
	mc := m.selectConn()
	if mc == nil {
		t.Fatal("selectConn should return a conn even with zero latencies")
	}
}

func newTestMux(n int) *Mux {
	m := &Mux{
		inFrames: make(chan *Frame, 100),
		allDead:  make(chan struct{}),
		connDied: make(chan int, 16),
		logger:   slog.Default(),
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	for i := 0; i < n; i++ {
		m.conns = append(m.conns, &muxConn{conn: &dummyConn{}})
	}
	return m
}
