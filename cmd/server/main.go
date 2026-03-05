package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/call-vpn/call-vpn/internal/server"
)

func main() {
	listenAddr := flag.String("listen", "0.0.0.0:9000", "DTLS UDP listen address")
	authToken := flag.String("token", "", "client auth token (env: VPN_TOKEN, empty = no auth)")
	callLink := flag.String("link", "", "VK call link ID for relay-to-relay mode")
	useTCP := flag.Bool("tcp", true, "use TCP for TURN connections (relay mode)")
	flag.Parse()

	if *authToken == "" {
		*authToken = os.Getenv("VPN_TOKEN")
	}
	if *callLink == "" {
		*callLink = os.Getenv("VK_CALL_LINK")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := server.Config{
		ListenAddr: *listenAddr,
		AuthToken:  *authToken,
		CallLink:   *callLink,
		UseTCP:     *useTCP,
		Logger:     logger,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := server.New(cfg)
	srv.Start(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
		logger.Info("shutting down server")
		srv.Stop()
	case <-srv.Done():
	}
}
