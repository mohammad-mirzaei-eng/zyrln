package mitm

import (
	"testing"

	"zyrln/relay/appscript"
	"zyrln/relay/route"
)

// desktopMode mirrors the stack diagram labels.
func desktopMode(directOn bool, coal *appscript.Coalescer) proxyMode {
	orig := route.GetDirectEnabled()
	route.SetDirectEnabled(directOn)
	defer route.SetDirectEnabled(orig)
	return currentMode(coal, nil)
}

func TestRoutingMatrix_DesktopModes(t *testing.T) {
	fakeCoal := appscript.NewCoalescer(nil, []string{"http://127.0.0.1/"}, "www.google.com", "k", 0)

	tests := []struct {
		name     string
		directOn bool
		coal     *appscript.Coalescer
		want     proxyMode
	}{
		{"nothing: direct OFF, no URLs", false, nil, modeDisconnected},
		{"direct only: direct ON, no URLs", true, nil, modeDirect},
		{"relay direct OFF", false, fakeCoal, modeRelay},
		{"relay direct ON", true, fakeCoal, modeDirectRelay},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := desktopMode(tc.directOn, tc.coal); got != tc.want {
				t.Fatalf("mode = %v, want %v", got, tc.want)
			}
		})
	}
	fakeCoal.Stop()
}

func TestRoutingMatrix_DesktopCONNECT_Outcomes(t *testing.T) {
	route.EnsureDomesticRules()
	route.SetDirectEnabled(true)

	// After bypass, modeDirect plain-pipes RouteRelay hosts (youtube in direct-only).
	if route.ConnectRouteForHost("www.youtube.com") != route.RouteRelay {
		t.Fatal("youtube must be relay route")
	}
	if route.ConnectRouteForHost("digikala.com") != route.RouteDomesticPlain {
		t.Fatal("digikala must bypass")
	}
	if route.ConnectRouteForHost("www.google.com") != route.RouteDirectFragment {
		t.Fatal("google must fragment when direct on")
	}

	route.SetDirectEnabled(false)
	if route.ConnectRouteForHost("www.google.com") != route.RouteRelay {
		t.Fatal("google must relay when direct off")
	}
}

func TestRoutingMatrix_SOCKSBackend_DirectRelay(t *testing.T) {
	route.SetDirectEnabled(true)
	defer route.SetDirectEnabled(true)

	if _, ok := dialSOCKSBackend(modeDirectRelay, "digikala.com", "digikala.com:443"); !ok {
		t.Fatal("digikala should bypass MITM via DialPlainDirect")
	}
	if _, ok := dialSOCKSBackend(modeDirectRelay, "www.google.com", "www.google.com:443"); !ok {
		t.Fatal("google should bypass MITM via DialFragment")
	}
	if _, ok := dialSOCKSBackend(modeDirectRelay, "www.youtube.com", "www.youtube.com:443"); ok {
		t.Fatal("youtube should fall through to MITM (nil,false)")
	}
}

func TestRoutingMatrix_SOCKSBackend_RelayDirectOff(t *testing.T) {
	route.SetDirectEnabled(false)
	defer route.SetDirectEnabled(true)

	if _, ok := dialSOCKSBackend(modeRelay, "www.google.com", "www.google.com:443"); ok {
		t.Fatal("google with direct OFF should use MITM, not fragment bypass in modeRelay")
	}
	if _, ok := dialSOCKSBackend(modeRelay, "digikala.com", "digikala.com:443"); !ok {
		t.Fatal("digikala should still plain bypass in modeRelay")
	}
}
