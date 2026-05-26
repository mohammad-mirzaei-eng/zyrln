package route

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"

	"zyrln/relay/conn"
)

func startLocalEcho(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				io.Copy(conn, conn)
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestConnFromReadWriter(t *testing.T) {
	c1, _ := net.Pipe()
	defer c1.Close()
	bc := conn.NewBufferedConn(c1, bufio.NewReader(c1))
	if got := ConnFromReadWriter(bc); got == nil {
		t.Fatal("expected BufferedConn as net.Conn")
	}
	if ConnFromReadWriter(struct{ io.ReadWriter }{c1}) != nil {
		t.Fatal("bare ReadWriter without Conn should return nil")
	}
}

func TestApplyBypassConnect_DomesticPlain(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	done := make(chan bool, 1)
	go func() {
		ok := ApplyBypassConnect(server, "127.0.0.1:1", RouteDomesticPlain)
		done <- ok
	}()

	br := bufio.NewReader(client)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "502") && !strings.Contains(line, "200") {
		t.Fatalf("expected proxy response, got %q", line)
	}
	if !<-done {
		t.Fatal("expected bypass handled")
	}
}

func TestApplyBypassConnect_RelayReturnsFalse(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	if ApplyBypassConnect(c2, "example.com:443", RouteRelay) {
		t.Fatal("relay route should not bypass")
	}
}

func TestPipe_Bidirectional(t *testing.T) {
	a, b := net.Pipe()

	msg := []byte("pipe-test")
	go func() {
		Pipe(a, b)
	}()
	if _, err := a.Write(msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(b, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("got %q", buf)
	}
	_ = a.Close()
	_ = b.Close()
}

func TestDialProtectedTCPConn_LocalEcho(t *testing.T) {
	echo := startLocalEcho(t)
	c, ok := DialProtectedTCPConn(echo)
	if !ok || c == nil {
		t.Fatal("expected dial ok")
	}
	defer c.Close()
}

func TestHandlePlainConnect_BadHost(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	go HandlePlainConnect(c2, "127.0.0.1:1") // likely refused
	br := bufio.NewReader(c1)
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "502") {
		t.Fatalf("want 502 on dial fail, got %q", line)
	}
}
