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

func TestPool_AuthTokenValidation(t *testing.T) {
	rig := testrig.New(t, testrig.Options{TURNServers: 1, Calls: 1})
	logger := testLogger(t)

	// --- Sub-test 1: matching tokens should work ---
	t.Run("MatchingTokens", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		serverPool := tunnel.NewCallPool(tunnel.PoolConfig{
			Services:     []provider.Service{rig.Service(0)},
			ConnsPerCall: 1,
			AuthToken:    "correct",
			Logger:       logger,
		})
		defer serverPool.Close()

		serverCh := startServerPool(ctx, serverPool)
		time.Sleep(3 * time.Second)

		clientPool := tunnel.NewCallPool(tunnel.PoolConfig{
			Services:     []provider.Service{rig.Service(0)},
			ConnsPerCall: 1,
			AuthToken:    "correct",
			Logger:       logger,
		})
		defer clientPool.Close()

		clientMux, err := clientPool.StartClient(ctx)
		if err != nil {
			t.Fatalf("client start with correct token: %v", err)
		}

		serverResult := <-serverCh
		if serverResult.err != nil {
			t.Fatalf("server start: %v", serverResult.err)
		}

		if clientMux.ActiveConns() < 1 {
			t.Fatalf("client MUX active conns = %d, want >= 1", clientMux.ActiveConns())
		}
		verifyDataTransfer(t, clientMux, serverResult.m)
	})

	// --- Sub-test 2: mismatched token should fail ---
	t.Run("WrongToken", func(t *testing.T) {
		// Short timeout: wrong token means no connection will succeed.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		serverPool := tunnel.NewCallPool(tunnel.PoolConfig{
			Services:     []provider.Service{rig.Service(0)},
			ConnsPerCall: 1,
			AuthToken:    "correct",
			Logger:       logger,
		})
		defer serverPool.Close()

		_ = startServerPool(ctx, serverPool)
		time.Sleep(3 * time.Second)

		clientPool := tunnel.NewCallPool(tunnel.PoolConfig{
			Services:     []provider.Service{rig.Service(0)},
			ConnsPerCall: 1,
			AuthToken:    "wrong",
			Logger:       logger,
		})
		defer clientPool.Close()

		_, err := clientPool.StartClient(ctx)
		if err == nil {
			t.Fatal("expected error when using wrong auth token, got nil")
		}
		t.Logf("correctly rejected with wrong token: %v", err)
	})
}

func TestPool_GracefulDegradation(t *testing.T) {
	// Both sides have 2 calls, but TURN server 1 is killed after server connects.
	// Client cannot allocate on call 1, server blocks until context timeout on slot 1.
	// The pool should still work on call 0.
	rig := testrig.New(t, testrig.Options{TURNServers: 2, Calls: 2})
	logger := testLogger(t)

	// Use a moderate timeout: slot 0 completes fast, slot 1 times out via context.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	// Wait for server to connect and pre-allocate on both slots.
	time.Sleep(8 * time.Second)

	// Kill TURN server 1 so client's slot 1 allocation fails.
	rig.KillTURN(1)

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

	// Client should have at least 1 connection (call 0 succeeded).
	if clientMux.ActiveConns() < 1 {
		t.Fatalf("client MUX active conns = %d, want >= 1", clientMux.ActiveConns())
	}

	serverResult := <-serverCh
	if serverResult.err != nil {
		t.Fatalf("server start: %v", serverResult.err)
	}

	if serverResult.m.ActiveConns() < 1 {
		t.Fatalf("server MUX active conns = %d, want >= 1", serverResult.m.ActiveConns())
	}

	verifyDataTransfer(t, clientMux, serverResult.m)
}

func TestPool_MUXCreationRace(t *testing.T) {
	t.Parallel()

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

	// Both slots should have contributed connections.
	if clientMux.ActiveConns() < 2 {
		t.Fatalf("client MUX active conns = %d, want >= 2", clientMux.ActiveConns())
	}
	if serverResult.m.ActiveConns() < 2 {
		t.Fatalf("server MUX active conns = %d, want >= 2", serverResult.m.ActiveConns())
	}

	// If run with -race, the race detector validates no data races in MUX creation.
	t.Log("no data races detected in MUX creation with 2 simultaneous slots")
}

func TestPool_CloseRace(t *testing.T) {
	t.Parallel()

	rig := testrig.New(t, testrig.Options{TURNServers: 2, Calls: 2})
	logger := testLogger(t)

	services := []provider.Service{rig.Service(0), rig.Service(1)}

	// Start server pool in background.
	serverCtx, serverCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer serverCancel()

	serverPool := tunnel.NewCallPool(tunnel.PoolConfig{
		Services:     services,
		ConnsPerCall: 1,
		AuthToken:    "test123",
		Logger:       logger,
	})

	go func() {
		_, _ = serverPool.StartServer(serverCtx)
	}()

	// Start client pool in background.
	clientCtx, clientCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer clientCancel()

	clientPool := tunnel.NewCallPool(tunnel.PoolConfig{
		Services:     services,
		ConnsPerCall: 1,
		AuthToken:    "test123",
		Logger:       logger,
	})

	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		_, _ = clientPool.StartClient(clientCtx)
	}()

	// Close immediately while connections are still being established.
	// This tests that Close() doesn't panic or deadlock during setup.
	time.Sleep(500 * time.Millisecond)
	clientPool.Close()
	serverPool.Close()

	// Wait for client goroutine to finish (should not deadlock).
	select {
	case <-clientDone:
		t.Log("client pool closed cleanly during connection setup")
	case <-time.After(15 * time.Second):
		t.Fatal("deadlock: client pool did not finish after Close()")
	}
}
