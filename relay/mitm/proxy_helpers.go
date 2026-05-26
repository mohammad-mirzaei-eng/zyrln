package mitm

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"zyrln/relay/appscript"
	"zyrln/relay/netdial"
	"zyrln/relay/route"
)

func directRoundTripTransport() *http.Transport {
	return &http.Transport{
		DialContext:     netdial.ProtectedDialer(15 * time.Second).DialContext,
		IdleConnTimeout: 30 * time.Second,
	}
}

func copyForwardedRequestHeaders(dst *http.Request, src *http.Header) {
	for k, vs := range *src {
		if !skipRequestHeader(k) {
			for _, v := range vs {
				dst.Header.Add(k, v)
			}
		}
	}
}

func writeRelayHTTPResponse(w io.Writer, relayResp appscript.RelayResponse, setClose bool) error {
	resp := &http.Response{
		StatusCode:    relayResp.Status,
		Status:        fmt.Sprintf("%d %s", relayResp.Status, http.StatusText(relayResp.Status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        make(http.Header),
		Body:          io.NopCloser(bytes.NewReader(relayResp.Body)),
		ContentLength: int64(len(relayResp.Body)),
	}
	for k, vs := range relayResp.Headers {
		if !skipResponseHeader(k) {
			for _, v := range vs {
				resp.Header.Add(k, v)
			}
		}
	}
	if setClose {
		resp.Header.Set("Connection", "close")
	}
	return resp.Write(w)
}

// dialSOCKSBackend handles SOCKS TLS streams that bypass MITM (direct / domestic / plain).
// Returns (conn, true) when the SOCKS client should be piped to conn; (nil, false) → use MITM.
func dialSOCKSBackend(mode proxyMode, certHost, targetHost string) (net.Conn, bool) {
	switch mode {
	case modeDirect:
		switch route.ConnectRouteForHost(certHost) {
		case route.RouteDirectFragment:
			return route.DialFragment(targetHost)
		case route.RouteDomesticPlain:
			return route.DialPlainDirect(targetHost)
		default:
			return route.DialProtectedTCPConn(targetHost)
		}
	case modeDirectRelay:
		switch route.ConnectRouteForHost(certHost) {
		case route.RouteDirectFragment:
			return route.DialFragment(targetHost)
		case route.RouteDomesticPlain:
			return route.DialPlainDirect(targetHost)
		}
		return nil, false
	case modeRelay:
		if route.ShouldUseDomesticBypass(certHost) {
			return route.DialPlainDirect(targetHost)
		}
		return nil, false
	default:
		return nil, false
	}
}
