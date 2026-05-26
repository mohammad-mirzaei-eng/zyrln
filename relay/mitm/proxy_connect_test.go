package mitm

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	connutil "zyrln/relay/conn"
	"zyrln/relay/route"
)

func startEchoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo server listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(conn, conn)
	}()
	return ln.Addr().String()
}

func saveDirectEnabled(t *testing.T) func() {
	t.Helper()
	orig := route.GetDirectEnabled()
	return func() { route.SetDirectEnabled(orig) }
}

func startRelayProxy(t *testing.T, urls []string, frontDomain string, ca *CertAuthority, client *http.Client) (addr string, cleanup func()) {
	t.Helper()
	if client == nil {
		client = http.DefaultClient
	}
	srv, ln, err := StartProxy("127.0.0.1:0", urls, frontDomain, "k", ca, client, 5*time.Second)
	if err != nil {
		t.Fatalf("StartProxy: %v", err)
	}
	return ln.Addr().String(), func() {
		_ = ln.Close()
		_ = srv.Close()
	}
}

func dialCONNECT(t *testing.T, proxyAddr, host string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", resp.StatusCode)
	}
	return conn, br
}

func tlsClientForCA(t *testing.T, certPath, serverName string) *tls.Config {
	t.Helper()
	pemBytes, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("no PEM block in CA cert")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return &tls.Config{
		RootCAs:    pool,
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
	}
}

// --- mode / handleHTTP ---

func TestHandleHTTP_Disconnected_Returns502(t *testing.T) {
	defer saveDirectEnabled(t)()
	route.SetDirectEnabled(false)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	handleHTTP(w, r, nil, nil)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	if !strings.Contains(w.Body.String(), "no proxy configured") {
		t.Fatalf("body = %q, want no proxy configured", w.Body.String())
	}
}

func TestHandleHTTP_DirectMode_ForwardsPlainHTTP(t *testing.T) {
	defer saveDirectEnabled(t)()
	route.SetDirectEnabled(true)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hello" {
			t.Errorf("path = %q, want /hello", r.URL.Path)
		}
		_, _ = w.Write([]byte("direct-ok"))
	}))
	defer backend.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, backend.URL+"/hello", nil)
	handleHTTP(w, r, nil, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); body != "direct-ok" {
		t.Fatalf("body = %q, want direct-ok", body)
	}
}

// --- handleConnect ---

func TestHandleConnect_Disconnected_Returns502(t *testing.T) {
	defer saveDirectEnabled(t)()
	route.SetDirectEnabled(false)

	addr, cleanup := startRelayProxy(t, nil, "www.google.com", nil, nil)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	raw, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := string(raw)
	if !strings.Contains(out, "502") || !strings.Contains(out, "No proxy configured") {
		t.Fatalf("response = %q, want 502 No proxy configured", out)
	}
}

func TestHandleConnect_RelayWithoutCA_Returns502(t *testing.T) {
	defer saveDirectEnabled(t)()
	route.SetDirectEnabled(false)

	srv := fakeAppScript(t, "ok", 200)
	defer srv.Close()

	addr, cleanup := startRelayProxy(t, []string{srv.URL}, srv.Listener.Addr().String(), nil, srv.Client())
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	raw, _ := io.ReadAll(conn)
	out := string(raw)
	if !strings.Contains(out, "502") || !strings.Contains(out, "HTTPS proxy unavailable") {
		t.Fatalf("response = %q, want HTTPS proxy unavailable", out)
	}
}

func TestHandleConnect_DirectMode_PipesNonGoogle(t *testing.T) {
	defer saveDirectEnabled(t)()
	route.SetDirectEnabled(true)

	echoAddr := startEchoServer(t)
	host := echoAddr
	if h, p, err := net.SplitHostPort(echoAddr); err == nil {
		host = net.JoinHostPort(h, p)
	}

	addr, cleanup := startRelayProxy(t, nil, "www.google.com", nil, nil)
	defer cleanup()

	conn, br := dialCONNECT(t, addr, host)
	tlsOff := connutil.NewBufferedConn(conn, br)

	payload := []byte("ping-through-tunnel")
	if _, err := tlsOff.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(tlsOff, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo = %q, want %q", got, payload)
	}
}

func TestHandleConnect_RelayMITM_RelaysHTTPS(t *testing.T) {
	defer saveDirectEnabled(t)()
	route.SetDirectEnabled(false)

	srv := fakeAppScript(t, "mitm-body", 200)
	defer srv.Close()

	certPath, keyPath := generateTestCA(t)
	ca, err := LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}

	addr, cleanup := startRelayProxy(t, []string{srv.URL}, srv.Listener.Addr().String(), ca, srv.Client())
	defer cleanup()

	conn, br := dialCONNECT(t, addr, "example.com:443")
	tlsConn := tls.Client(connutil.NewBufferedConn(conn, br), tlsClientForCA(t, certPath, "example.com"))
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("TLS handshake: %v", err)
	}
	defer tlsConn.Close()

	if _, err := fmt.Fprintf(tlsConn, "GET /path HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatalf("write GET: %v", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "mitm-body" {
		t.Fatalf("body = %q, want mitm-body", body)
	}
}

func TestHandleConnect_MITM_SSE_NoRelay(t *testing.T) {
	defer saveDirectEnabled(t)()
	route.SetDirectEnabled(false)

	var relayCalls int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayCalls++
		json.NewEncoder(w).Encode(struct {
			Status int    `json:"s"`
			Body   string `json:"b"`
		}{Status: 200, Body: ""})
	}))
	defer srv.Close()

	certPath, keyPath := generateTestCA(t)
	ca, err := LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}

	addr, cleanup := startRelayProxy(t, []string{srv.URL}, srv.Listener.Addr().String(), ca, srv.Client())
	defer cleanup()

	conn, br := dialCONNECT(t, addr, "example.com:443")
	tlsConn := tls.Client(connutil.NewBufferedConn(conn, br), tlsClientForCA(t, certPath, "example.com"))
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("TLS handshake: %v", err)
	}
	defer tlsConn.Close()

	if _, err := fmt.Fprintf(tlsConn, "GET /events HTTP/1.1\r\nHost: example.com\r\nAccept: text/event-stream\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatalf("write SSE request: %v", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if relayCalls != 0 {
		t.Fatalf("relay called %d times, want 0 for SSE", relayCalls)
	}
}
