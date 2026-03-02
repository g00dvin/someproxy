package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/call-vpn/call-vpn/internal/monitoring"
	"github.com/call-vpn/call-vpn/internal/mux"
	sig "github.com/call-vpn/call-vpn/internal/signal"
)

func main() {
	// Relay mode flags (default)
	signalAddr := flag.String("signal", "0.0.0.0:9443", "Signal server listen address")
	psk := flag.String("psk", "", "Pre-shared key for client authentication")

	// Direct mode flags (legacy)
	directMode := flag.Bool("direct", false, "Use direct TCP mode (legacy)")
	listenAddr := flag.String("listen", "0.0.0.0:9000", "Direct mode: TCP listen address")

	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	siren := monitoring.New(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutting down server")
		cancel()
	}()

	if *directMode {
		runDirectMode(ctx, logger, siren, *listenAddr)
	} else {
		if *psk == "" {
			fmt.Fprintln(os.Stderr, "Error: --psk is required in relay mode")
			fmt.Fprintln(os.Stderr, "Usage: server --psk=<secret> [--signal=0.0.0.0:9443]")
			fmt.Fprintln(os.Stderr, "       server --direct [--listen=0.0.0.0:9000]")
			os.Exit(1)
		}
		runRelayMode(ctx, logger, siren, *signalAddr, *psk)
	}
}

// runRelayMode starts the signaling server for TURN relay-based VPN sessions.
func runRelayMode(ctx context.Context, logger *slog.Logger, siren *monitoring.Siren, signalAddr, psk string) {
	srv := &sig.Server{
		Addr:   signalAddr,
		PSK:    psk,
		Logger: logger.With("component", "signal"),
		OnSession: func(ctx context.Context, sessionID string, m *mux.Mux) {
			sessionLogger := logger.With("session_id", sessionID)
			sessionLogger.Info("relay session started")

			for {
				stream, err := m.AcceptStream(ctx)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					sessionLogger.Warn("accept stream error", "err", err)
					siren.AlertDisconnect(ctx, fmt.Sprintf("session-%s", sessionID))
					return
				}
				go handleStream(ctx, sessionLogger, stream)
			}
		},
	}

	if err := srv.ListenAndServe(ctx); err != nil {
		logger.Error("signal server error", "err", err)
		os.Exit(1)
	}
}

// runDirectMode starts the legacy direct TCP server.
func runDirectMode(ctx context.Context, logger *slog.Logger, siren *monitoring.Siren, listenAddr string) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		logger.Error("failed to listen", "err", err)
		os.Exit(1)
	}
	defer ln.Close()

	logger.Info("server listening (direct mode)", "addr", listenAddr)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	var clientID atomic.Uint64

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Warn("accept error", "err", err)
			continue
		}
		id := clientID.Add(1)
		logger.Info("client connected", "client_id", id, "remote", conn.RemoteAddr())
		go handleClient(ctx, logger, siren, conn, id)
	}
}

func handleClient(ctx context.Context, logger *slog.Logger, siren *monitoring.Siren, conn net.Conn, clientID uint64) {
	defer conn.Close()

	clientLogger := logger.With("client_id", clientID)

	m := mux.New(clientLogger, conn)
	defer m.Close()

	for {
		stream, err := m.AcceptStream(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			clientLogger.Warn("accept stream error", "err", err)
			siren.AlertDisconnect(ctx, fmt.Sprintf("client-%d", clientID))
			return
		}
		go handleStream(ctx, clientLogger, stream)
	}
}

func handleStream(ctx context.Context, logger *slog.Logger, stream *mux.Stream) {
	defer stream.Close()

	addrBuf := make([]byte, 512)
	n, err := stream.Read(addrBuf)
	if err != nil {
		logger.Debug("read target address failed", "err", err)
		return
	}
	target := string(addrBuf[:n])

	logger.Debug("connecting to target", "stream_id", stream.ID, "target", target)

	dialer := net.Dialer{}
	outConn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		logger.Warn("dial target failed", "target", target, "err", err)
		return
	}
	defer outConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(outConn, stream)
	}()

	go func() {
		defer wg.Done()
		io.Copy(stream, outConn)
	}()

	wg.Wait()
}
