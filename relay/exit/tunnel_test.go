package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleTunnel_OpenTXRXClose(t *testing.T) {
	echo, echoAddr := startEchoServer(t)
	defer echo.Close()

	hub := newTunnelHub(time.Minute)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleTunnel(w, r, hub, "", time.Second)
	}))
	defer srv.Close()

	id := "test-session-1"
	targetHost := echoAddr

	openBody, _ := json.Marshal(tunnelRequest{Op: "open", ID: id, Target: targetHost})
	resp := postTunnel(t, srv.URL, openBody)
	if !resp.OK || resp.Error != "" {
		t.Fatalf("open: %+v", resp)
	}

	msg := []byte("hello tunnel")
	txBody, _ := json.Marshal(tunnelRequest{
		Op:   "tx",
		ID:   id,
		Data: base64.StdEncoding.EncodeToString(msg),
	})
	resp = postTunnel(t, srv.URL, txBody)
	if !resp.OK {
		t.Fatalf("tx: %+v", resp)
	}

	rxBody, _ := json.Marshal(tunnelRequest{Op: "rx", ID: id, WaitMS: 200})
	resp = postTunnel(t, srv.URL, rxBody)
	if !resp.OK {
		t.Fatalf("rx: %+v", resp)
	}
	got, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(msg) {
		t.Fatalf("rx data = %q want %q", got, msg)
	}

	closeBody, _ := json.Marshal(tunnelRequest{Op: "close", ID: id})
	resp = postTunnel(t, srv.URL, closeBody)
	if !resp.OK {
		t.Fatalf("close: %+v", resp)
	}
}

func TestHandleTunnel_Batch(t *testing.T) {
	echo, echoAddr := startEchoServer(t)
	defer echo.Close()

	hub := newTunnelHub(time.Minute)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleTunnel(w, r, hub, "", time.Second)
	}))
	defer srv.Close()

	id := "batch-session"
	batch, _ := json.Marshal(tunnelBatchRequest{Ops: []tunnelRequest{
		{Op: "open", ID: id, Target: echoAddr},
		{Op: "tx", ID: id, Data: base64.StdEncoding.EncodeToString([]byte("ping"))},
		{Op: "rx", ID: id, WaitMS: 200},
		{Op: "close", ID: id},
	}})

	res := postTunnelRaw(t, srv.URL, batch)
	var out tunnelBatchResponse
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 4 {
		t.Fatalf("results len = %d want 4", len(out.Results))
	}
	for i, r := range out.Results {
		if !r.OK {
			t.Fatalf("op %d failed: %+v", i, r)
		}
	}
}

func TestTunnelDialNetwork(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{"example.com:443", "tcp"},
		{"www.youtube.com:443", "tcp"},
		{"1.2.3.4:443", "tcp4"},
		{"[2001:db8::1]:443", "tcp"},
	}
	for _, tc := range tests {
		if got := tunnelDialNetwork(tc.target); got != tc.want {
			t.Errorf("tunnelDialNetwork(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

func TestHandleTunnel_Unauthorized(t *testing.T) {
	hub := newTunnelHub(time.Minute)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleTunnel(w, r, hub, "secret", time.Second)
	}))
	defer srv.Close()

	body, _ := json.Marshal(tunnelRequest{Op: "open", ID: "x", Target: "127.0.0.1:1"})
	resp := postTunnel(t, srv.URL, body)
	if resp.Error != "unauthorized" {
		t.Fatalf("got %+v want unauthorized", resp)
	}
}

func TestHandleTunnel_DuplicateSession(t *testing.T) {
	echo, echoAddr := startEchoServer(t)
	defer echo.Close()

	hub := newTunnelHub(time.Minute)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleTunnel(w, r, hub, "", time.Second)
	}))
	defer srv.Close()

	id := "dup-id"
	openBody, _ := json.Marshal(tunnelRequest{Op: "open", ID: id, Target: echoAddr})
	if resp := postTunnel(t, srv.URL, openBody); !resp.OK {
		t.Fatalf("first open: %+v", resp)
	}
	if resp := postTunnel(t, srv.URL, openBody); resp.Error != "session exists" {
		t.Fatalf("second open: %+v want session exists", resp)
	}
}

func startEchoServer(t *testing.T) (net.Listener, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buf := make([]byte, 4096)
				for {
					n, err := conn.Read(buf)
					if n > 0 {
						_, _ = conn.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return ln, ln.Addr().String()
}

func postTunnel(t *testing.T, baseURL string, body []byte) tunnelResponse {
	t.Helper()
	raw := postTunnelRaw(t, baseURL, body)
	var resp tunnelResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	return resp
}

func postTunnelRaw(t *testing.T, baseURL string, body []byte) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/"), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(res.Body); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
