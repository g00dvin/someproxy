package signal

// ConnectRequest is sent by the client to the signaling server to initiate
// a relay-based VPN session. The client has already created its own TURN
// allocations and sends their relay addresses so the server can target them.
type ConnectRequest struct {
	CallLink         string   `json:"call_link"`
	ClientRelayAddrs []string `json:"client_relay_addrs"`
	PSK              string   `json:"psk"`
	UseTCP           bool     `json:"use_tcp"`
}

// ConnectResponse is returned by the signaling server after it creates
// its own TURN allocations and is ready to exchange data.
type ConnectResponse struct {
	ServerRelayAddrs []string `json:"server_relay_addrs"`
	SessionID        string   `json:"session_id"`
}

// ErrorResponse wraps an error message for JSON responses.
type ErrorResponse struct {
	Error string `json:"error"`
}
