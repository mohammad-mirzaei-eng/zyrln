package mitm

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"zyrln/relay/appscript"
	connutil "zyrln/relay/conn"
	"zyrln/relay/log"
	"zyrln/relay/netdial"
	"zyrln/relay/route"
)

// proxyMode is the operating mode determined once per request from directEnabled + relay availability.
type proxyMode int

const (
	modeDisconnected proxyMode = iota // direct=off, no relay  → block all
	modeDirect                        // direct=on,  no relay  → frag Google, pipe others
	modeRelay                         // direct=off, relay=on  → MITM+relay all
	modeDirectRelay                   // direct=on,  relay=on  → frag Google, MITM+relay others
)

func currentMode(coal *appscript.Coalescer, ca *CertAuthority) proxyMode {
	direct := route.GetDirectEnabled()
	relay := coal != nil
	switch {
	case direct && relay:
		return modeDirectRelay
	case direct:
		return modeDirect
	case relay:
		return modeRelay
	default:
		return modeDisconnected
	}
}

// ServeProxyWithSOCKS starts the relay HTTP+HTTPS MITM proxy and a SOCKS5 listener.
// SOCKS5 support is limited to HTTP and HTTPS traffic so it can reuse the relay pipeline.
func ServeProxyWithSOCKS(httpListenAddr, socksListenAddr string, appScriptURLs []string, frontDomain, authKey string, ca *CertAuthority, client *http.Client, timeout time.Duration) error {
	coal, err := newProxyCoalescer(appScriptURLs, frontDomain, authKey, client, timeout)
	if err != nil {
		return err
	}
	httpSrv := buildHTTPProxyServer(httpListenAddr, coal, ca)
	if coal != nil {
		httpSrv.RegisterOnShutdown(coal.Stop)
	}
	socksSrv := NewSOCKSServer(socksListenAddr, coal, ca)

	errCh := make(chan error, 2)
	go func() {
		errCh <- httpSrv.ListenAndServe()
	}()
	go func() {
		errCh <- socksSrv.ListenAndServe()
	}()

	return <-errCh
}

// StartProxy starts the relay proxy in the background and returns the server and listener for shutdown.
// appScriptURLs is tried in order; the first that succeeds is used for each request.
// Close the returned listener (or call server.Close) to stop the proxy.
func StartProxy(listenAddr string, appScriptURLs []string, frontDomain, authKey string, ca *CertAuthority, client *http.Client, timeout time.Duration) (*http.Server, net.Listener, error) {
	srv, _, err := StartProxyWithCoalescer(listenAddr, appScriptURLs, frontDomain, authKey, ca, client, timeout)
	if err != nil {
		return nil, nil, err
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, nil, err
	}
	go func() { _ = srv.Serve(ln) }()
	return srv, ln, nil
}

// StartDirectProxy builds an HTTP CONNECT proxy in direct-only mode: fragmented
// TLS to Google domains, plain pipe for everything else. No relay, no MITM.
func StartDirectProxy(listenAddr string) *http.Server {
	route.EnsureDomesticRules()
	return buildHTTPProxyServer(listenAddr, nil, nil)
}

// StartProxyWithCoalescer is like StartProxy but also returns the Coalescer so
// callers can reuse it for ping/warmup without creating a separate HTTP client.
func StartProxyWithCoalescer(listenAddr string, appScriptURLs []string, frontDomain, authKey string, ca *CertAuthority, client *http.Client, timeout time.Duration) (*http.Server, *appscript.Coalescer, error) {
	coal, err := newProxyCoalescer(appScriptURLs, frontDomain, authKey, client, timeout)
	if err != nil {
		return nil, nil, err
	}
	srv := buildHTTPProxyServer(listenAddr, coal, ca)
	if coal != nil {
		srv.RegisterOnShutdown(coal.Stop)
	}
	return srv, coal, nil
}

// StartProxyWithSOCKS starts the relay HTTP proxy and a SOCKS5 listener in the background.
// Close the returned listeners and servers to stop both endpoints.
func StartProxyWithSOCKS(httpListenAddr, socksListenAddr string, appScriptURLs []string, frontDomain, authKey string, ca *CertAuthority, client *http.Client, timeout time.Duration) (*http.Server, net.Listener, *SOCKSServer, net.Listener, error) {
	srv, ln, socksSrv, socksLn, _, err := StartProxyWithSOCKSAndCoalescer(httpListenAddr, socksListenAddr, appScriptURLs, frontDomain, authKey, ca, client, timeout)
	return srv, ln, socksSrv, socksLn, err
}

// StartProxyWithSOCKSAndCoalescer is like StartProxyWithSOCKS but also returns the Coalescer.
func StartProxyWithSOCKSAndCoalescer(httpListenAddr, socksListenAddr string, appScriptURLs []string, frontDomain, authKey string, ca *CertAuthority, client *http.Client, timeout time.Duration) (*http.Server, net.Listener, *SOCKSServer, net.Listener, *appscript.Coalescer, error) {
	coal, err := newProxyCoalescer(appScriptURLs, frontDomain, authKey, client, timeout)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	httpSrv := buildHTTPProxyServer(httpListenAddr, coal, ca)
	if coal != nil {
		httpSrv.RegisterOnShutdown(coal.Stop)
	}
	socksSrv := NewSOCKSServer(socksListenAddr, coal, ca)

	httpLn, err := net.Listen("tcp", httpListenAddr)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	socksLn, err := net.Listen("tcp", socksListenAddr)
	if err != nil {
		_ = httpLn.Close()
		return nil, nil, nil, nil, nil, err
	}

	go func() { _ = httpSrv.Serve(httpLn) }()
	go func() { _ = socksSrv.Serve(socksLn) }()
	return httpSrv, httpLn, socksSrv, socksLn, coal, nil
}

func newProxyCoalescer(appScriptURLs []string, frontDomain, authKey string, client *http.Client, timeout time.Duration) (*appscript.Coalescer, error) {
	if len(appScriptURLs) == 0 {
		return nil, nil
	}
	coal := appscript.NewCoalescer(client, appScriptURLs, frontDomain, authKey, timeout)
	coal.Warmup()
	route.EnsureDomesticRules()
	return coal, nil
}

func buildHTTPProxyServer(listenAddr string, coal *appscript.Coalescer, ca *CertAuthority) *http.Server {
	return &http.Server{
		Addr: listenAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				handleConnect(w, r, coal, ca)
			} else {
				handleHTTP(w, r, coal, ca)
			}
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func handleHTTP(w http.ResponseWriter, r *http.Request, coal *appscript.Coalescer, ca *CertAuthority) {
	targetURL := r.URL.String()
	if !r.URL.IsAbs() {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		targetURL = scheme + "://" + r.Host + r.URL.RequestURI()
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, appscript.MaxProxyRequestBody))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	switch currentMode(coal, ca) {
	case modeDisconnected:
		http.Error(w, "no proxy configured", http.StatusBadGateway)
		return
	case modeDirect:
		directHTTP(w, r, targetURL, body)
		return
	case modeRelay, modeDirectRelay:
		if route.ShouldUseDomesticBypass(route.HostFromConnectTarget(hostFromHTTPRequest(r))) {
			directHTTP(w, r, targetURL, body)
			return
		}
		if coal == nil {
			http.Error(w, "no proxy configured", http.StatusBadGateway)
			return
		}
	}

	relayResp, err := coal.Submit(r.Method, targetURL, forwardHeaders(r.Header), body)
	if err != nil {
		http.Error(w, "relay failed: "+err.Error(), http.StatusBadGateway)
		log.Logf("error", "%s %s → relay error: %s", r.Method, targetURL, err)
		return
	}

	for k, vs := range relayResp.Headers {
		if !skipResponseHeader(k) {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
	}
	w.WriteHeader(relayResp.Status)
	_, _ = w.Write(relayResp.Body)
	log.Logf("info", "%s %s → %d %s", r.Method, targetURL, relayResp.Status, log.FmtBytes(len(relayResp.Body)))
}

func applyConnectBypass(conn net.Conn, host, targetHostport string) bool {
	return route.ApplyBypassConnect(conn, targetHostport, route.ConnectRouteForHost(host))
}

func hostFromHTTPRequest(r *http.Request) string {
	if r.URL != nil && r.URL.Host != "" {
		return r.URL.Host
	}
	return r.Host
}

// directHTTP forwards a plain HTTP request directly to the target (no relay).
// Used in direct-only mode so non-Google HTTP traffic isn't blocked.
func directHTTP(w http.ResponseWriter, r *http.Request, targetURL string, body []byte) {
	req, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	copyForwardedRequestHeaders(req, &r.Header)
	resp, err := directRoundTripTransport().RoundTrip(req)
	if err != nil {
		http.Error(w, "direct failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func directHTTPToConn(conn net.Conn, r *http.Request, targetURL string, body []byte) error {
	req, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	copyForwardedRequestHeaders(req, &r.Header)
	resp, err := directRoundTripTransport().RoundTrip(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return resp.Write(conn)
}

func handleConnect(w http.ResponseWriter, r *http.Request, coal *appscript.Coalescer, ca *CertAuthority) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}

	rawConn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer rawConn.Close()

	certHost, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		certHost = r.Host
	}
	certHost = strings.TrimSpace(certHost)
	if certHost == "" {
		_, _ = rawConn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	switch currentMode(coal, ca) {
	case modeDisconnected:
		_, _ = rawConn.Write([]byte("HTTP/1.1 502 No proxy configured\r\n\r\n"))
		return
	case modeDirect:
		if applyConnectBypass(rawConn, certHost, r.Host) {
			return
		}
		serverConn, err := netdial.ProtectedDialer(15 * time.Second).DialContext(r.Context(), "tcp", r.Host)
		if err != nil {
			_, _ = rawConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			return
		}
		defer serverConn.Close()
		_, _ = rawConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		route.Pipe(rawConn, serverConn)
		return
	case modeDirectRelay, modeRelay:
		if applyConnectBypass(rawConn, certHost, r.Host) {
			return
		}
	}

	if ca == nil {
		_, _ = rawConn.Write([]byte("HTTP/1.1 502 HTTPS proxy unavailable\r\n\r\n"))
		return
	}

	_, _ = rawConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	cert, err := ca.CertForHost(certHost)
	if err != nil {
		log.Logf("error", "TLS cert %s: %v", certHost, err)
		return
	}

	tlsConn := tls.Server(rawConn, &tls.Config{
		Certificates: []tls.Certificate{*cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	defer tlsConn.Close()
	handleMITMTLS(tlsConn, certHost, r.Host, coal)
}

func handleMITMTLS(tlsConn net.Conn, certHost, targetHost string, coal *appscript.Coalescer) {

	reader := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Logf("error", "MITM read %s: %v", certHost, err)
			}
			return
		}

		// SSE connections are persistent streams that Apps Script cannot relay.
		// Respond immediately and keep the connection alive with comment keepalives
		// so the browser never detects a dead stream and triggers a page refresh.
		if strings.Contains(req.Header.Get("Accept"), "text/event-stream") {
			serveSSEKeepalive(tlsConn)
			return
		}

		body, err := io.ReadAll(io.LimitReader(req.Body, appscript.MaxProxyRequestBody))
		_ = req.Body.Close()
		if err != nil {
			_, _ = tlsConn.Write([]byte("HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n"))
			return
		}

		targetURL := "https://" + targetHost + req.URL.RequestURI()
		if route.ShouldUseDomesticBypass(certHost) {
			if err := directHTTPToConn(tlsConn, req, targetURL, body); err != nil {
				connutil.WriteHTTPError(tlsConn, http.StatusBadGateway, "direct failed: "+err.Error())
				return
			}
			if strings.EqualFold(req.Header.Get("Connection"), "close") {
				return
			}
			continue
		}
		if coal == nil {
			connutil.WriteHTTPError(tlsConn, http.StatusBadGateway, "no proxy configured")
			return
		}
		relayResp, err := coal.Submit(req.Method, targetURL, forwardHeaders(req.Header), body)
		if err != nil {
			connutil.WriteHTTPError(tlsConn, http.StatusBadGateway, "relay failed: "+err.Error())
			log.Logf("error", "%s %s → relay error: %s", req.Method, targetURL, err)
			return
		}
		if err := writeRelayHTTPResponse(tlsConn, relayResp, strings.EqualFold(req.Header.Get("Connection"), "close")); err != nil {
			return
		}
		log.Logf("info", "%s %s → %d %s", req.Method, targetURL, relayResp.Status, log.FmtBytes(len(relayResp.Body)))

		if strings.EqualFold(req.Header.Get("Connection"), "close") {
			return
		}
	}
}

// SOCKSServer exposes the HTTP relay pipeline behind a SOCKS5 handshake.
// It supports CONNECT for HTTP and HTTPS traffic and rejects UDP and BIND.
type SOCKSServer struct {
	Addr string
	coal *appscript.Coalescer
	ca   *CertAuthority
}

// NewSOCKSServer creates a SOCKS5 server that forwards HTTP and HTTPS through the relay.
func NewSOCKSServer(addr string, coal *appscript.Coalescer, ca *CertAuthority) *SOCKSServer {
	return &SOCKSServer{Addr: addr, coal: coal, ca: ca}
}

// ListenAndServe starts the SOCKS5 server on its configured address.
func (s *SOCKSServer) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// Serve accepts SOCKS5 client connections on ln.
func (s *SOCKSServer) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleConn(conn)
	}
}

func (s *SOCKSServer) handleConn(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	targetHost, err := s.handshake(reader, conn)
	if err != nil {
		return
	}

	if connutil.IsLikelyTLS(reader) {
		certHost := route.HostFromConnectTarget(targetHost)
		if certHost == "" {
			return
		}
		mode := currentMode(s.coal, s.ca)
		if mode == modeDisconnected {
			return
		}
		if serverConn, ok := dialSOCKSBackend(mode, certHost, targetHost); ok {
			defer serverConn.Close()
			route.Pipe(connutil.NewBufferedConn(conn, reader), serverConn)
			return
		}
		if s.ca == nil {
			return
		}

		cert, err := s.ca.CertForHost(certHost)
		if err != nil {
			log.Logf("error", "SOCKS TLS cert %s: %v", certHost, err)
			return
		}

		tlsConn := tls.Server(connutil.NewBufferedConn(conn, reader), &tls.Config{
			Certificates: []tls.Certificate{*cert},
			MinVersion:   tls.VersionTLS12,
		})
		defer tlsConn.Close()
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		handleMITMTLS(tlsConn, certHost, targetHost, s.coal)
		return
	}

	handleSOCKSHTTP(connutil.NewBufferedConn(conn, reader), targetHost, currentMode(s.coal, s.ca), s.coal)
}

func (s *SOCKSServer) handshake(reader *bufio.Reader, conn net.Conn) (string, error) {
	version, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	if version != 0x05 {
		return "", fmt.Errorf("unsupported socks version %d", version)
	}

	methodCount, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	methods := make([]byte, int(methodCount))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return "", err
	}

	selected := byte(0xFF)
	for _, method := range methods {
		if method == 0x00 {
			selected = 0x00
			break
		}
	}
	if _, err := conn.Write([]byte{0x05, selected}); err != nil {
		return "", err
	}
	if selected == 0xFF {
		return "", fmt.Errorf("no acceptable socks auth method")
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", err
	}
	if header[0] != 0x05 {
		return "", fmt.Errorf("unsupported socks request version %d", header[0])
	}
	if header[1] != 0x01 {
		s.writeReply(conn, 0x07, nil)
		return "", fmt.Errorf("unsupported socks command %d", header[1])
	}

	host, err := connutil.ReadSOCKSAddress(reader, header[3])
	if err != nil {
		s.writeReply(conn, 0x08, nil)
		return "", err
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return "", err
	}
	targetHost := net.JoinHostPort(host, fmt.Sprintf("%d", binary.BigEndian.Uint16(portBytes)))
	if err := s.writeReply(conn, 0x00, conn.LocalAddr()); err != nil {
		return "", err
	}
	return targetHost, nil
}

func (s *SOCKSServer) writeReply(conn net.Conn, status byte, addr net.Addr) error {
	ip := net.IPv4zero
	port := uint16(0)
	if tcpAddr, ok := addr.(*net.TCPAddr); ok {
		if v4 := tcpAddr.IP.To4(); v4 != nil {
			ip = v4
		}
		port = uint16(tcpAddr.Port)
	}
	reply := []byte{0x05, status, 0x00, 0x01, ip[0], ip[1], ip[2], ip[3], 0x00, 0x00}
	binary.BigEndian.PutUint16(reply[len(reply)-2:], port)
	_, err := conn.Write(reply)
	return err
}

func handleSOCKSHTTP(conn net.Conn, targetHost string, mode proxyMode, coal *appscript.Coalescer) {
	defer conn.Close()

	switch mode {
	case modeDisconnected:
		connutil.WriteHTTPError(conn, http.StatusBadGateway, "no proxy configured")
		return
	case modeDirect:
		serverConn, ok := route.DialProtectedTCPConn(targetHost)
		if !ok {
			connutil.WriteHTTPError(conn, http.StatusBadGateway, "direct failed")
			return
		}
		defer serverConn.Close()
		route.Pipe(conn, serverConn)
		return
	case modeRelay, modeDirectRelay:
	}

	reader := bufio.NewReader(conn)
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Logf("error", "SOCKS read %s: %v", targetHost, err)
			}
			return
		}

		body, err := io.ReadAll(io.LimitReader(req.Body, appscript.MaxProxyRequestBody))
		_ = req.Body.Close()
		if err != nil {
			connutil.WriteHTTPError(conn, http.StatusBadRequest, "read body failed")
			return
		}

		host := targetHost
		if req.Host != "" {
			host = req.Host
			if !strings.Contains(host, ":") && strings.Contains(targetHost, ":") {
				_, port, err := net.SplitHostPort(targetHost)
				if err == nil && port != "80" {
					host = net.JoinHostPort(host, port)
				}
			}
		}

		targetURL := "http://" + host + req.URL.RequestURI()
		if route.ShouldUseDomesticBypass(route.HostFromConnectTarget(host)) {
			if err := directHTTPToConn(conn, req, targetURL, body); err != nil {
				connutil.WriteHTTPError(conn, http.StatusBadGateway, "direct failed: "+err.Error())
				return
			}
			if strings.EqualFold(req.Header.Get("Connection"), "close") {
				return
			}
			continue
		}
		if coal == nil {
			connutil.WriteHTTPError(conn, http.StatusBadGateway, "no proxy configured")
			return
		}
		relayResp, err := coal.Submit(req.Method, targetURL, forwardHeaders(req.Header), body)
		if err != nil {
			connutil.WriteHTTPError(conn, http.StatusBadGateway, "relay failed: "+err.Error())
			log.Logf("error", "%s %s → relay error: %s", req.Method, targetURL, err)
			return
		}
		if err := writeRelayHTTPResponse(conn, relayResp, strings.EqualFold(req.Header.Get("Connection"), "close")); err != nil {
			return
		}
		log.Logf("info", "%s %s → %d %s", req.Method, targetURL, relayResp.Status, log.FmtBytes(len(relayResp.Body)))

		if strings.EqualFold(req.Header.Get("Connection"), "close") {
			return
		}
	}
}

func forwardHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k, vs := range h {
		if !skipRequestHeader(k) && len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	if _, ok := out["User-Agent"]; !ok {
		out["User-Agent"] = "zyrln/0.1"
	}
	return out
}

func skipRequestHeader(key string) bool {
	switch strings.ToLower(key) {
	case "host", "connection", "content-length", "proxy-connection",
		"proxy-authorization", "transfer-encoding", "accept-encoding",
		"x-forwarded-for", "x-real-ip", "via":
		return true
	}
	return false
}

// serveSSEKeepalive handles a Server-Sent Events request by holding the
// connection open and writing SSE comment keepalives every 20s. No relay call
// is made, so no Apps Script quota is used. The browser sees a live stream
// and does not trigger a page refresh when the real SSE endpoint is unreachable.

func serveSSEKeepalive(conn net.Conn) {
	_, err := fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nCache-Control: no-cache\r\nConnection: keep-alive\r\n\r\n")
	if err != nil {
		return
	}
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if _, err := fmt.Fprintf(conn, ": keepalive\n\n"); err != nil {
			return
		}
	}
}

func skipResponseHeader(key string) bool {
	switch strings.ToLower(key) {
	case "content-length", "transfer-encoding", "connection", "content-encoding":
		return true
	}
	return false
}
