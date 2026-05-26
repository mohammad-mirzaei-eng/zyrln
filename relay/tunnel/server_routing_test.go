package tunnel

import (
	"bufio"
	"net"
	"strings"
	"testing"

	"zyrln/relay/route"
)

// Domestic must bypass before tunnel-nil check (digikala → plain, not "No tunnel configured").
func TestHandleRelayTunnelConnect_DigikalaBypassBeforeTunnel(t *testing.T) {
	route.EnsureDomesticRules()
	if !route.ShouldUseDomesticBypass("digikala.com") {
		t.Fatal("digikala.com must be domestic")
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleRelayTunnelConnect(server, "digikala.com:443", &tunnelBundle{})
	}()

	br := bufio.NewReader(client)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(line, "No tunnel configured") {
		t.Fatalf("domestic must bypass before tunnel check; got %q", line)
	}
	<-done
}

// Google + direct ON must not hit tunnel when client is nil (fragment path or dial error, not tunnel 502).
func TestHandleRelayTunnelConnect_GoogleDirectOnSkipsTunnel(t *testing.T) {
	route.SetDirectEnabled(true)
	defer route.SetDirectEnabled(true)

	if route.ConnectRouteForHost("www.google.com") != route.RouteDirectFragment {
		t.Fatal("expected fragment route")
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleRelayTunnelConnect(server, "www.google.com:443", &tunnelBundle{})
	}()

	br := bufio.NewReader(client)
	line, _ := br.ReadString('\n')
	if strings.Contains(line, "No tunnel configured") {
		t.Fatalf("google fragment must bypass tunnel; got %q", line)
	}
	<-done
}
