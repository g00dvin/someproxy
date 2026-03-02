package signal

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/call-vpn/call-vpn/internal/mux"
	"github.com/call-vpn/call-vpn/internal/turn"
	"github.com/google/uuid"
)

// OnSessionFunc is called when a new relay session is established.
// The callback receives the session context, ID, and a ready-to-use Mux.
type OnSessionFunc func(ctx context.Context, sessionID string, m *mux.Mux)

// Server is the signaling HTTP server that accepts client connect requests,
// creates server-side TURN allocations, and establishes relay-to-relay sessions.
type Server struct {
	Addr      string
	PSK       string
	Logger    *slog.Logger
	OnSession OnSessionFunc

	httpServer *http.Server
}

// ListenAndServe starts the signaling HTTP server with graceful shutdown.
func (s *Server) ListenAndServe(ctx context.Context) error {
	muxHTTP := http.NewServeMux()
	muxHTTP.HandleFunc("/signal", s.handleSignal)
	muxHTTP.HandleFunc("/health", s.handleHealth)

	s.httpServer = &http.Server{
		Addr:         s.Addr,
		Handler:      muxHTTP,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 150 * time.Second, // long timeout for TURN allocation
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(shutdownCtx)
	}()

	s.Logger.Info("signal server listening", "addr", s.Addr)
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Close shuts down the HTTP server.
func (s *Server) Close() error {
	if s.httpServer != nil {
		return s.httpServer.Close()
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleSignal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "read body failed")
		return
	}

	var req ConnectRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Validate PSK
	if subtle.ConstantTimeCompare([]byte(s.PSK), []byte(req.PSK)) != 1 {
		s.Logger.Warn("invalid PSK from client")
		s.writeError(w, http.StatusUnauthorized, "invalid psk")
		return
	}

	if req.CallLink == "" {
		s.writeError(w, http.StatusBadRequest, "call_link required")
		return
	}
	if len(req.ClientRelayAddrs) == 0 {
		s.writeError(w, http.StatusBadRequest, "client_relay_addrs required")
		return
	}

	sessionID := uuid.New().String()
	n := len(req.ClientRelayAddrs)
	s.Logger.Info("signal request received",
		"session_id", sessionID,
		"client_relays", n,
		"call_link", req.CallLink,
	)

	// Parse client relay addresses
	clientAddrs := make([]*net.UDPAddr, n)
	for i, addrStr := range req.ClientRelayAddrs {
		addr, err := net.ResolveUDPAddr("udp", addrStr)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid client relay addr %d: %v", i, err))
			return
		}
		clientAddrs[i] = addr
	}

	// Create server-side TURN allocations
	mgr := turn.NewManager(req.CallLink, req.UseTCP, s.Logger)
	allocs, err := mgr.Allocate(r.Context(), n)
	if err != nil {
		s.Logger.Error("server TURN allocation failed", "err", err)
		s.writeError(w, http.StatusInternalServerError, "TURN allocation failed")
		mgr.CloseAll()
		return
	}

	if len(allocs) < n {
		s.Logger.Warn("partial TURN allocation", "requested", n, "got", len(allocs))
		// Proceed with what we got, client will adjust
		n = len(allocs)
	}

	// Build DatagramConn pairs: server relay[i] ↔ client relay addr[i]
	conns := make([]io.ReadWriteCloser, n)
	for i := 0; i < n; i++ {
		dc := mux.NewDatagramConn(allocs[i].RelayConn, clientAddrs[i])
		// Send permission probe so TURN server creates permission for client relay
		if err := dc.SendProbe(); err != nil {
			s.Logger.Warn("permission probe failed", "index", i, "err", err)
		}
		conns[i] = dc
	}

	// Create Mux over the DatagramConn pairs
	muxLogger := s.Logger.With("session_id", sessionID)
	m := mux.New(muxLogger, conns...)

	// Collect server relay addresses for the response
	serverRelayAddrs := make([]string, n)
	for i := 0; i < n; i++ {
		serverRelayAddrs[i] = allocs[i].RelayAddr.String()
	}

	// Respond to client with server relay addresses
	resp := ConnectResponse{
		ServerRelayAddrs: serverRelayAddrs,
		SessionID:        sessionID,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.Logger.Error("write response failed", "err", err)
		m.Close()
		mgr.CloseAll()
		return
	}

	s.Logger.Info("signal response sent",
		"session_id", sessionID,
		"server_relays", n,
	)

	// Launch session handler in background
	sessionCtx, sessionCancel := context.WithCancel(context.Background())

	go func() {
		defer sessionCancel()
		defer m.Close()
		defer mgr.CloseAll()

		// Start ping loop to keep TURN permissions alive
		go m.StartPingLoop(sessionCtx, 30*time.Second)

		if s.OnSession != nil {
			s.OnSession(sessionCtx, sessionID, m)
		}
	}()
}

func (s *Server) writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(ErrorResponse{Error: msg})
}
