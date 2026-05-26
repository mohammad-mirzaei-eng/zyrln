package route

import "testing"

func TestConnectRouteForHost(t *testing.T) {
	SetDirectEnabled(true)
	defer SetDirectEnabled(true)

	if got := ConnectRouteForHost("www.google.com"); got != RouteDirectFragment {
		t.Fatalf("google = %v, want direct", got)
	}
	if got := ConnectRouteForHost("shop.example.ir"); got != RouteDomesticPlain {
		t.Fatalf(".ir = %v, want domestic", got)
	}
	if got := ConnectRouteForHost("www.youtube.com"); got != RouteRelay {
		t.Fatalf("youtube = %v, want relay", got)
	}

	SetDirectEnabled(false)
	if got := ConnectRouteForHost("www.google.com"); got != RouteRelay {
		t.Fatalf("google direct off = %v, want relay", got)
	}
}

func TestHostFromConnectTarget(t *testing.T) {
	if got := HostFromConnectTarget("www.example.com:443"); got != "www.example.com" {
		t.Fatalf("host:port = %q", got)
	}
	if got := HostFromConnectTarget("example.com"); got != "example.com" {
		t.Fatalf("bare = %q", got)
	}
}
