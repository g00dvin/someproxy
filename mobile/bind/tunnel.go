// Package bind provides gomobile bindings for the VPN tunnel core.
// Build with: gomobile bind -target android ./mobile/bind/
//             gomobile bind -target ios ./mobile/bind/
package bind

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/call-vpn/call-vpn/internal/mux"
	sig "github.com/call-vpn/call-vpn/internal/signal"
	"github.com/call-vpn/call-vpn/internal/turn"
)

// Tunnel is the main gomobile-exported type for mobile platforms.
type Tunnel struct {
	mu         sync.Mutex
	mgr        *turn.Manager
	m          *mux.Mux
	logger     *slog.Logger
	cancel     context.CancelFunc
	nextStream atomic.Uint32
	running    bool
}

// TunnelConfig holds configuration for starting the tunnel.
type TunnelConfig struct {
	CallLink   string // VK call-link ID
	ServerAddr string // legacy direct mode: host:port
	SignalURL  string // relay mode: signaling server URL
	PSK        string // relay mode: pre-shared key
	NumConns   int    // parallel TURN connections
	UseTCP     bool   // TCP vs UDP for TURN
	DirectMode bool   // true = legacy direct TCP mode
}

// NewTunnel creates a new tunnel instance.
func NewTunnel() *Tunnel {
	return &Tunnel{
		logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
}

// Start establishes TURN connections and starts the mux tunnel.
// Returns nil on success, error string on failure.
func (t *Tunnel) Start(cfg *TunnelConfig) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return fmt.Errorf("tunnel already running")
	}

	if cfg.DirectMode {
		return t.startDirect(cfg)
	}
	return t.startRelay(cfg)
}

// startRelay establishes a relay-to-relay session via signaling.
func (t *Tunnel) startRelay(cfg *TunnelConfig) error {
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel

	t.mgr = turn.NewManager(cfg.CallLink, cfg.UseTCP, t.logger)

	allocs, err := t.mgr.Allocate(ctx, cfg.NumConns)
	if err != nil {
		cancel()
		return fmt.Errorf("allocate TURN: %w", err)
	}

	// Collect client relay addresses
	clientRelayAddrs := make([]string, len(allocs))
	for i, a := range allocs {
		clientRelayAddrs[i] = a.RelayAddr.String()
	}

	// Signal server
	resp, err := sig.Connect(ctx, cfg.SignalURL, sig.ConnectRequest{
		CallLink:         cfg.CallLink,
		ClientRelayAddrs: clientRelayAddrs,
		PSK:              cfg.PSK,
		UseTCP:           cfg.UseTCP,
	})
	if err != nil {
		t.mgr.CloseAll()
		cancel()
		return fmt.Errorf("signal connect: %w", err)
	}

	// Build DatagramConn pairs
	n := len(resp.ServerRelayAddrs)
	if n > len(allocs) {
		n = len(allocs)
	}

	conns := make([]io.ReadWriteCloser, n)
	for i := 0; i < n; i++ {
		serverRelayAddr, err := net.ResolveUDPAddr("udp", resp.ServerRelayAddrs[i])
		if err != nil {
			t.mgr.CloseAll()
			cancel()
			return fmt.Errorf("resolve server relay %d: %w", i, err)
		}
		dc := mux.NewDatagramConn(allocs[i].RelayConn, serverRelayAddr)
		if err := dc.SendProbe(); err != nil {
			t.logger.Warn("permission probe failed", "index", i, "err", err)
		}
		conns[i] = dc
	}

	// Brief wait for permission probes
	time.Sleep(500 * time.Millisecond)

	t.m = mux.New(t.logger, conns...)
	go t.m.DispatchLoop(ctx)
	go t.m.StartPingLoop(ctx, 30*time.Second)

	t.running = true
	t.logger.Info("tunnel started (relay mode)", "connections", n, "session_id", resp.SessionID)
	return nil
}

// startDirect establishes a direct connection to the VPN server (legacy mode).
func (t *Tunnel) startDirect(cfg *TunnelConfig) error {
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel

	t.mgr = turn.NewManager(cfg.CallLink, cfg.UseTCP, t.logger)

	allocs, err := t.mgr.Allocate(ctx, cfg.NumConns)
	if err != nil {
		cancel()
		return fmt.Errorf("allocate TURN: %w", err)
	}

	serverAddr, err := net.ResolveUDPAddr("udp", cfg.ServerAddr)
	if err != nil {
		t.mgr.CloseAll()
		cancel()
		return fmt.Errorf("resolve server: %w", err)
	}

	conns := make([]io.ReadWriteCloser, len(allocs))
	for i, a := range allocs {
		conns[i] = mux.NewConn(a.RelayConn, serverAddr)
	}

	t.m = mux.New(t.logger, conns...)
	go t.m.DispatchLoop(ctx)

	t.running = true
	t.logger.Info("tunnel started (direct mode)", "connections", len(allocs))
	return nil
}

// DialStream opens a new mux stream to the given target address (host:port).
// The returned ReadWriteCloser can be used to send/receive data.
func (t *Tunnel) DialStream(addr string) (io.ReadWriteCloser, error) {
	t.mu.Lock()
	m := t.m
	t.mu.Unlock()

	if m == nil {
		return nil, fmt.Errorf("tunnel not running")
	}

	id := t.nextStream.Add(1)
	stream, err := m.OpenStream(id)
	if err != nil {
		return nil, err
	}
	if _, err := stream.Write([]byte(addr)); err != nil {
		stream.Close()
		return nil, err
	}
	return stream, nil
}

// WritePacket sends a raw IP packet through the tunnel (for VpnService / NEPacketTunnelProvider).
func (t *Tunnel) WritePacket(data []byte) error {
	t.mu.Lock()
	m := t.m
	t.mu.Unlock()

	if m == nil {
		return fmt.Errorf("tunnel not running")
	}

	return m.SendFrame(&mux.Frame{
		StreamID: 0, // Stream 0 = raw IP packets
		Type:     mux.FrameData,
		Sequence: m.NextSeq(),
		Length:   uint32(len(data)),
		Payload:  data,
	})
}

// ReadPacket reads a raw IP packet from the tunnel.
func (t *Tunnel) ReadPacket(buf []byte) (int, error) {
	t.mu.Lock()
	m := t.m
	t.mu.Unlock()

	if m == nil {
		return 0, fmt.Errorf("tunnel not running")
	}

	frame, ok := <-m.RecvFrames()
	if !ok {
		return 0, fmt.Errorf("tunnel closed")
	}
	n := copy(buf, frame.Payload)
	return n, nil
}

// Stop tears down all connections.
func (t *Tunnel) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running {
		return
	}

	if t.cancel != nil {
		t.cancel()
	}
	if t.m != nil {
		t.m.Close()
	}
	if t.mgr != nil {
		t.mgr.CloseAll()
	}
	t.running = false
	t.logger.Info("tunnel stopped")
}

// IsRunning returns whether the tunnel is active.
func (t *Tunnel) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}
