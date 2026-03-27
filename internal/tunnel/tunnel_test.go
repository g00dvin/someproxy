package tunnel_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/call-vpn/call-vpn/internal/mux"
	"github.com/call-vpn/call-vpn/internal/provider"
	"github.com/call-vpn/call-vpn/internal/testrig"
	"github.com/call-vpn/call-vpn/internal/tunnel"
)

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	if os.Getenv("TUNNEL_TEST_VERBOSE") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// startServerPool starts a server pool in a goroutine and returns a channel
// that receives the MUX (or nil) + error once StartServer completes.
func startServerPool(ctx context.Context, pool *tunnel.CallPool) <-chan struct {
	m   *mux.Mux
	err error
} {
	ch := make(chan struct {
		m   *mux.Mux
		err error
	}, 1)
	go func() {
		m, err := pool.StartServer(ctx)
		ch <- struct {
			m   *mux.Mux
			err error
		}{m, err}
	}()
	return ch
}

// verifyDataTransfer opens a stream on client MUX, accepts it on server MUX,
// sends data, and verifies it arrives correctly.
func verifyDataTransfer(t *testing.T, clientMux, serverMux *mux.Mux) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serverMux.EnableStreamAccept(16)
	go serverMux.DispatchLoop(ctx)

	// Client opens a stream.
	stream, err := clientMux.OpenStream(42)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	payload := []byte("hello from tunnel pool test")
	if _, err := stream.Write(payload); err != nil {
		t.Fatalf("stream write: %v", err)
	}

	// Server accepts the stream.
	select {
	case srv := <-serverMux.AcceptedStreams():
		buf := make([]byte, 256)
		n, err := srv.Read(buf)
		if err != nil {
			t.Fatalf("stream read: %v", err)
		}
		if string(buf[:n]) != string(payload) {
			t.Fatalf("data mismatch: got %q, want %q", buf[:n], payload)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for accepted stream")
	}
}

func TestPool_SingleSlot(t *testing.T) {
	rig := testrig.New(t, testrig.Options{TURNServers: 1, Calls: 1})
	logger := testLogger(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Server pool — start in background (blocks waiting for client).
	serverPool := tunnel.NewCallPool(tunnel.PoolConfig{
		Services:     []provider.Service{rig.Service(0)},
		ConnsPerCall: 1,
		AuthToken:    "test123",
		Logger:       logger,
	})
	defer serverPool.Close()

	serverCh := startServerPool(ctx, serverPool)

	// Give server time to connect and pre-allocate.
	time.Sleep(3 * time.Second)

	// Client pool.
	clientPool := tunnel.NewCallPool(tunnel.PoolConfig{
		Services:     []provider.Service{rig.Service(0)},
		ConnsPerCall: 1,
		AuthToken:    "test123",
		Logger:       logger,
	})
	defer clientPool.Close()

	clientMux, err := clientPool.StartClient(ctx)
	if err != nil {
		t.Fatalf("client start: %v", err)
	}

	// Wait for server to finish.
	serverResult := <-serverCh
	if serverResult.err != nil {
		t.Fatalf("server start: %v", serverResult.err)
	}

	if clientMux.ActiveConns() < 1 {
		t.Fatalf("client MUX active conns = %d, want >= 1", clientMux.ActiveConns())
	}
	if serverResult.m.ActiveConns() < 1 {
		t.Fatalf("server MUX active conns = %d, want >= 1", serverResult.m.ActiveConns())
	}

	verifyDataTransfer(t, clientMux, serverResult.m)
}

func TestPool_TwoSlots(t *testing.T) {
	rig := testrig.New(t, testrig.Options{TURNServers: 2, Calls: 2})
	logger := testLogger(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	services := []provider.Service{rig.Service(0), rig.Service(1)}

	serverPool := tunnel.NewCallPool(tunnel.PoolConfig{
		Services:     services,
		ConnsPerCall: 1,
		AuthToken:    "test123",
		Logger:       logger,
	})
	defer serverPool.Close()

	serverCh := startServerPool(ctx, serverPool)

	// Server connects 2 slots sequentially with slotConnectDelay between them.
	// Wait long enough for both to connect + pre-allocate.
	time.Sleep(8 * time.Second)

	clientPool := tunnel.NewCallPool(tunnel.PoolConfig{
		Services:     services,
		ConnsPerCall: 1,
		AuthToken:    "test123",
		Logger:       logger,
	})
	defer clientPool.Close()

	clientMux, err := clientPool.StartClient(ctx)
	if err != nil {
		t.Fatalf("client start: %v", err)
	}

	serverResult := <-serverCh
	if serverResult.err != nil {
		t.Fatalf("server start: %v", serverResult.err)
	}

	if clientMux.ActiveConns() < 2 {
		t.Fatalf("client MUX active conns = %d, want >= 2", clientMux.ActiveConns())
	}
	if serverResult.m.ActiveConns() < 2 {
		t.Fatalf("server MUX active conns = %d, want >= 2", serverResult.m.ActiveConns())
	}

	verifyDataTransfer(t, clientMux, serverResult.m)
}

func TestPool_TwoSlots_MultiConn(t *testing.T) {
	rig := testrig.New(t, testrig.Options{TURNServers: 2, Calls: 2})
	logger := testLogger(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	services := []provider.Service{rig.Service(0), rig.Service(1)}

	serverPool := tunnel.NewCallPool(tunnel.PoolConfig{
		Services:     services,
		ConnsPerCall: 2,
		AuthToken:    "test123",
		Logger:       logger,
	})
	defer serverPool.Close()

	serverCh := startServerPool(ctx, serverPool)

	// 2 slots x 2 conns each = 4 total. Give plenty of time for sequential connects.
	time.Sleep(8 * time.Second)

	clientPool := tunnel.NewCallPool(tunnel.PoolConfig{
		Services:     services,
		ConnsPerCall: 2,
		AuthToken:    "test123",
		Logger:       logger,
	})
	defer clientPool.Close()

	clientMux, err := clientPool.StartClient(ctx)
	if err != nil {
		t.Fatalf("client start: %v", err)
	}

	serverResult := <-serverCh
	if serverResult.err != nil {
		t.Fatalf("server start: %v", serverResult.err)
	}

	// 2 calls x 2 conns = 4 minimum.
	if clientMux.ActiveConns() < 4 {
		t.Fatalf("client MUX active conns = %d, want >= 4", clientMux.ActiveConns())
	}
	if serverResult.m.ActiveConns() < 4 {
		t.Fatalf("server MUX active conns = %d, want >= 4", serverResult.m.ActiveConns())
	}

	verifyDataTransfer(t, clientMux, serverResult.m)
}
