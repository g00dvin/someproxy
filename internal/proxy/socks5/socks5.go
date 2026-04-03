package socks5

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/call-vpn/call-vpn/internal/bypass"
)

// DialFunc establishes a connection to the target through the mux tunnel.
type DialFunc func(ctx context.Context, network, addr string) (io.ReadWriteCloser, error)

// Server implements a SOCKS5 proxy (RFC 1928).
type Server struct {
	Addr     string
	Dial     DialFunc
	Bypass   *bypass.Matcher
	Logger   *slog.Logger
	listener net.Listener
	streamID atomic.Uint32
}

// ListenAndServe starts the SOCKS5 proxy server.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("socks5 listen: %w", err)
	}
	s.listener = ln
	s.Logger.Info("SOCKS5 proxy listening", "addr", s.Addr)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.Logger.Warn("accept error", "err", err)
			continue
		}
		go s.handleConn(ctx, conn)
	}
}

// Close stops the listener.
func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	// Set a deadline for the SOCKS5 handshake to prevent stalls.
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	// 1. Auth negotiation
	if err := s.negotiate(conn); err != nil {
		s.Logger.Debug("socks5 negotiate failed", "err", err)
		return
	}

	// 2. Read request
	target, err := s.readRequest(conn)
	if err != nil {
		s.Logger.Debug("socks5 request failed", "err", err)
		return
	}

	// 3. Connect (bypass VPN for matched domains, tunnel otherwise)
	var remote io.ReadWriteCloser
	if s.Bypass != nil && s.Bypass.Match(target) {
		s.Logger.Debug("bypass tunnel", "target", target)
		c, err := net.DialTimeout("tcp", target, 10*time.Second)
		if err != nil {
			s.Logger.Warn("bypass dial failed", "target", target, "err", err)
			s.sendReply(conn, 0x05)
			return
		}
		remote = c
	} else {
		dialCtx, dialCancel := context.WithTimeout(ctx, 30*time.Second)
		c, err := s.Dial(dialCtx, "tcp", target)
		dialCancel()
		if err != nil {
			s.Logger.Warn("tunnel dial failed", "target", target, "err", err)
			s.sendReply(conn, 0x05)
			return
		}
		remote = c
	}
	defer remote.Close()

	// 4. Send success reply
	if err := s.sendReply(conn, 0x00); err != nil {
		return
	}

	// Clear deadline before relay — tunnel stays open indefinitely.
	conn.SetDeadline(time.Time{})

	// 5. Bidirectional copy
	s.relay(conn, remote)
}

func (s *Server) negotiate(conn net.Conn) error {
	// Read: VER | NMETHODS | METHODS
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if buf[0] != 0x05 {
		return errors.New("unsupported SOCKS version")
	}
	methods := make([]byte, buf[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	// Reply: VER | METHOD (no auth required)
	_, err := conn.Write([]byte{0x05, 0x00})
	return err
}

func (s *Server) readRequest(conn net.Conn) (string, error) {
	// VER | CMD | RSV | ATYP
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", err
	}
	if buf[0] != 0x05 {
		return "", errors.New("unsupported SOCKS version")
	}
	if buf[1] != 0x01 { // CONNECT
		s.sendReply(conn, 0x07) // Command not supported
		return "", fmt.Errorf("unsupported command: %d", buf[1])
	}

	var host string
	switch buf[3] {
	case 0x01: // IPv4
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", err
		}
		host = net.IP(addr).String()
	case 0x03: // Domain
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return "", err
		}
		domain := make([]byte, lenBuf[0])
		if _, err := io.ReadFull(conn, domain); err != nil {
			return "", err
		}
		host = string(domain)
	case 0x04: // IPv6
		addr := make([]byte, 16)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", err
		}
		host = net.IP(addr).String()
	default:
		return "", fmt.Errorf("unsupported address type: %d", buf[3])
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBuf)

	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func (s *Server) sendReply(conn net.Conn, rep byte) error {
	// VER | REP | RSV | ATYP | BND.ADDR | BND.PORT
	reply := []byte{0x05, rep, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	_, err := conn.Write(reply)
	return err
}

func (s *Server) relay(client net.Conn, remote io.ReadWriteCloser) {
	// When one direction finishes (EOF or error), close both connections
	// to unblock the other io.Copy goroutine.
	var once sync.Once
	closeAll := func() {
		client.Close()
		remote.Close()
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(remote, client)
		once.Do(closeAll)
	}()

	go func() {
		defer wg.Done()
		io.Copy(client, remote)
		once.Do(closeAll)
	}()

	wg.Wait()
}
