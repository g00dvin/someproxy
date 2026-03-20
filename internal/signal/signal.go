// Package signal defines wire-level constants used across providers
// for relay-to-relay reconnection signaling.
package signal

const (
	// Reconnection signaling tags.
	WireConnNew = "av-conn-new" // client → server: new relay addr for reconnect
	WireConnOk  = "av-conn-ok"  // server → client: reply relay addr
)
