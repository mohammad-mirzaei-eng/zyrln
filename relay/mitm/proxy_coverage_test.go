package mitm

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zyrln/relay/appscript"
	"zyrln/relay/route"
)

func TestStartDirectProxy_ReturnsServer(t *testing.T) {
	route.SetDirectEnabled(true)
	srv := StartDirectProxy("127.0.0.1:0")
	if srv == nil {
		t.Fatal("nil server")
	}
}

func TestDirectHTTPToConn(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok-body"))
	}))
	defer backend.Close()

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	readDone := make(chan struct{})
	go func() {
		serverReadHTTP(t, c1)
		close(readDone)
	}()

	req, _ := http.NewRequest(http.MethodGet, backend.URL, nil)
	if err := directHTTPToConn(c2, req, backend.URL, nil); err != nil {
		t.Fatal(err)
	}
	<-readDone
}

func serverReadHTTP(t *testing.T, c net.Conn) {
	t.Helper()
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok-body" {
		t.Fatalf("body = %q", body)
	}
}

func TestWriteRelayHTTPResponse_Close(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	resp := appscript.RelayResponse{
		Status:  200,
		Headers: map[string][]string{"Content-Type": {"text/plain"}},
		Body:    []byte("relay-ok"),
	}
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = writeRelayHTTPResponse(c, resp, true)
		_ = c.Close()
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(client)
	_ = client.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "relay-ok") || !strings.Contains(string(raw), "close") {
		t.Fatalf("response = %q", raw)
	}
}

func TestSOCKSServer_HandshakeIPv4(t *testing.T) {
	s := NewSOCKSServer("127.0.0.1:0", nil, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	type result struct {
		host string
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			resCh <- result{"", err}
			return
		}
		defer c.Close()
		host, err := s.handshake(bufio.NewReader(c), c)
		resCh <- result{host, err}
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	req := []byte{
		0x05, 0x01, 0x00,
		0x05, 0x01, 0x00, 0x01,
		127, 0, 0, 1,
		0, 9,
	}
	if _, err := client.Write(req); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	res := <-resCh
	if res.err != nil {
		t.Fatal(res.err)
	}
	if res.host != "127.0.0.1:9" {
		t.Fatalf("host = %q", res.host)
	}
}

func TestCopyForwardedRequestHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Test", "1")
	h.Set("Connection", "close")
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	copyForwardedRequestHeaders(req, &h)
	if req.Header.Get("X-Test") != "1" {
		t.Fatal("expected X-Test")
	}
	if req.Header.Get("Connection") != "" {
		t.Fatal("hop-by-hop should be stripped")
	}
}
