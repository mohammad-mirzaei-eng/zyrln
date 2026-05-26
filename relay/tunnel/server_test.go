package tunnel

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zyrln/relay/core"
)

func TestConnectHost(t *testing.T) {
	if got := core.HostFromConnectTarget("www.google.com:443"); got != "www.google.com" {
		t.Fatalf("HostFromConnectTarget = %q", got)
	}
}

func TestTunnelConnectRoute(t *testing.T) {
	core.SetDirectEnabled(true)
	defer core.SetDirectEnabled(true)

	if got := core.ConnectRouteForHost("shop.example.ir"); got != core.RouteDomesticPlain {
		t.Fatalf(".ir = %v, want domestic", got)
	}
	if got := core.ConnectRouteForHost("www.google.com"); got != core.RouteDirectFragment {
		t.Fatalf("google = %v, want direct", got)
	}
	if got := core.ConnectRouteForHost("www.youtube.com"); got != core.RouteRelay {
		t.Fatalf("youtube = %v, want relay", got)
	}
}

func TestHandleTunnelHTTP_RequiresCONNECT(t *testing.T) {
	tc := NewTunnelClient(http.DefaultClient, []string{"https://example.com"}, "", "k", time.Second)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://example.com/test", nil)
	handleTunnelHTTP(w, r, &tunnelBundle{tunnel: tc})

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}
