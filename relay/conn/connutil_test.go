package conn

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
)

func TestBufferedConn_ReadUsesReader(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() { _, _ = c2.Write([]byte("hello")) }()

	bc := NewBufferedConn(c1, bufio.NewReader(c1))
	buf := make([]byte, 5)
	n, err := bc.Read(buf)
	if err != nil || n != 5 || string(buf) != "hello" {
		t.Fatalf("Read = (%d, %v) buf=%q", n, err, buf[:n])
	}
}

func TestBufferedConnImplementsNetConn(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	bc := NewBufferedConn(c1, bufio.NewReader(c1))
	var rw interface{} = bc
	if _, ok := rw.(net.Conn); !ok {
		t.Fatal("BufferedConn should satisfy net.Conn")
	}
}

func TestWriteHTTPError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	done := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		WriteHTTPError(c, 502, "bad gateway")
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
	done <- raw
	out := string(<-done)
	if !strings.Contains(out, "502") || !strings.Contains(out, "bad gateway") {
		t.Fatalf("response = %q", out)
	}
}

func TestIsLikelyTLS(t *testing.T) {
	tlsHello := []byte{0x16, 0x03, 0x01}
	if !IsLikelyTLS(bufio.NewReader(bytes.NewReader(tlsHello))) {
		t.Fatal("expected TLS record 0x16")
	}
	if IsLikelyTLS(bufio.NewReader(bytes.NewReader([]byte("GET ")))) {
		t.Fatal("HTTP should not look like TLS")
	}
	if IsLikelyTLS(bufio.NewReader(bytes.NewReader(nil))) {
		t.Fatal("empty reader should not be TLS")
	}
}

func TestReadSOCKSAddress_IPv4(t *testing.T) {
	raw := []byte{1, 2, 3, 4}
	host, err := ReadSOCKSAddress(bytes.NewReader(raw), 0x01)
	if err != nil || host != "1.2.3.4" {
		t.Fatalf("ipv4 = %q, %v", host, err)
	}
}

func TestReadSOCKSAddress_Domain(t *testing.T) {
	raw := append([]byte{11}, []byte("example.com")...)
	host, err := ReadSOCKSAddress(bytes.NewReader(raw), 0x03)
	if err != nil || host != "example.com" {
		t.Fatalf("domain = %q, %v", host, err)
	}
}

func TestReadSOCKSAddress_Unsupported(t *testing.T) {
	_, err := ReadSOCKSAddress(bytes.NewReader(nil), 0x99)
	if err == nil {
		t.Fatal("expected error for unsupported atyp")
	}
}
