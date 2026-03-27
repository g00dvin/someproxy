package testrig

import (
	"fmt"
	"net"
	"testing"

	"github.com/call-vpn/call-vpn/internal/provider"
	"github.com/pion/turn/v4"
)

type TURNServer struct {
	Addr     string
	Username string
	Password string
	server   *turn.Server
	conn     net.PacketConn
}

// turnUsers is the set of credentials accepted by all testrig TURN servers.
// All servers accept all users so that round-robin allocation works across servers.
var turnUsers = map[string]string{
	"test0": "pass0",
	"test1": "pass1",
	"test2": "pass2",
	"test3": "pass3",
}

func newTURNServer(t *testing.T, index int) *TURNServer {
	t.Helper()

	username := fmt.Sprintf("test%d", index)
	password := fmt.Sprintf("pass%d", index)
	realm := "testrig"

	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testrig: listen UDP: %v", err)
	}

	// Pre-compute auth keys for all known users.
	authKeys := make(map[string][]byte, len(turnUsers))
	for u, p := range turnUsers {
		authKeys[u] = turn.GenerateAuthKey(u, realm, p)
	}

	srv, err := turn.NewServer(turn.ServerConfig{
		Realm: realm,
		AuthHandler: func(u string, realm string, srcAddr net.Addr) ([]byte, bool) {
			key, ok := authKeys[u]
			return key, ok
		},
		PacketConnConfigs: []turn.PacketConnConfig{
			{
				PacketConn: conn,
				RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{
					RelayAddress: net.ParseIP("127.0.0.1"),
					Address:      "127.0.0.1",
				},
			},
		},
	})
	if err != nil {
		conn.Close()
		t.Fatalf("testrig: start TURN server %d: %v", index, err)
	}

	addr := conn.LocalAddr().String()
	t.Logf("testrig: TURN server %d listening on %s (user=%s)", index, addr, username)

	return &TURNServer{
		Addr:     addr,
		Username: username,
		Password: password,
		server:   srv,
		conn:     conn,
	}
}

func (s *TURNServer) Credentials() *provider.Credentials {
	host, port, _ := net.SplitHostPort(s.Addr)
	return &provider.Credentials{
		Username: s.Username,
		Password: s.Password,
		Host:     host,
		Port:     port,
		Servers: []provider.TURNServer{
			{Host: host, Port: port},
		},
	}
}

func (s *TURNServer) Close() error {
	return s.server.Close()
}
