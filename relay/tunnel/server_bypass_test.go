package tunnel

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// TestHandleRelayTunnelConnect_ForeignWithoutTunnel returns 502 when relay path needed but no tunnel client.
func TestHandleRelayTunnelConnect_ForeignWithoutTunnel(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleRelayTunnelConnect(server, "example.com:443", &tunnelBundle{})
	}()

	br := bufio.NewReader(client)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(line, "502") {
		t.Fatalf("response = %q, want 502", line)
	}
	<-done
}
