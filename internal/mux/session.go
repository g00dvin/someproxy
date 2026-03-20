package mux

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
	"time"
)

const (
	sessionIDSize = 16
	nonceSize     = 16
	hmacSize      = 32
)

// WriteSessionID sends a 16-byte session UUID as the first data after
// a DTLS handshake. The server uses this to group multiple connections
// from the same client into a single MUX.
func WriteSessionID(conn io.Writer, sessionID [16]byte) error {
	_, err := conn.Write(sessionID[:])
	if err != nil {
		return fmt.Errorf("write session id: %w", err)
	}
	return nil
}

// ReadSessionID reads a 16-byte session UUID from a new connection.
// Applies a 10-second read deadline if the connection supports it.
func ReadSessionID(conn io.Reader) ([16]byte, error) {
	var id [16]byte

	// Set read deadline if supported.
	type deadliner interface {
		SetReadDeadline(time.Time) error
	}
	if d, ok := conn.(deadliner); ok {
		d.SetReadDeadline(time.Now().Add(10 * time.Second))
		defer d.SetReadDeadline(time.Time{})
	}

	if _, err := io.ReadFull(conn, id[:]); err != nil {
		return id, fmt.Errorf("read session id: %w", err)
	}
	return id, nil
}

// WriteAuthToken sends a 16-byte random nonce followed by HMAC-SHA256(key=token, data=nonce)
// (32 bytes) over the connection. Total: 48 bytes. The nonce prevents replay attacks.
func WriteAuthToken(conn io.Writer, token string) error {
	var nonce [nonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write(nonce[:])
	sig := mac.Sum(nil)

	if _, err := conn.Write(nonce[:]); err != nil {
		return fmt.Errorf("write nonce: %w", err)
	}
	if _, err := conn.Write(sig); err != nil {
		return fmt.Errorf("write hmac: %w", err)
	}
	return nil
}

// ValidateAuthToken reads a 16-byte nonce and 32-byte HMAC from the connection,
// recomputes HMAC-SHA256(key=expectedToken, data=nonce), and compares using constant-time comparison.
func ValidateAuthToken(conn io.Reader, expectedToken string) error {
	if c, ok := conn.(interface{ SetReadDeadline(time.Time) error }); ok {
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		defer c.SetReadDeadline(time.Time{})
	}

	var nonce [nonceSize]byte
	if _, err := io.ReadFull(conn, nonce[:]); err != nil {
		return fmt.Errorf("read nonce: %w", err)
	}

	var received [hmacSize]byte
	if _, err := io.ReadFull(conn, received[:]); err != nil {
		return fmt.Errorf("read hmac: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(expectedToken))
	mac.Write(nonce[:])
	expected := mac.Sum(nil)

	if subtle.ConstantTimeCompare(received[:], expected) != 1 {
		return fmt.Errorf("auth token mismatch")
	}
	return nil
}
