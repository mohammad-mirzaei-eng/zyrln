package appscript

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestExportedAPIWrappers(t *testing.T) {
	if got := PreviewBytes([]byte("hello world"), 8); got != "hello..." {
		t.Fatalf("PreviewBytes = %q", got)
	}
	if PerURLTimeout(30*time.Second, 3) <= 0 {
		t.Fatal("PerURLTimeout")
	}
	payload := BuildRelayPayload("k", "GET", "https://example.com", nil, nil)
	if payload == "" {
		t.Fatal("BuildRelayPayload")
	}
	client := NewHTTPClient(5 * time.Second)
	if client == nil {
		t.Fatal("NewHTTPClient")
	}
}

func TestCoalescer_Submit_Success(t *testing.T) {
	srv := mockAppsScriptServer(t, 200, "coal-body")
	defer srv.Close()

	c := NewCoalescer(srv.Client(), []string{srv.URL}, srvHost(srv), "k", 5*time.Second)
	defer c.Stop()

	resp, err := c.Submit("GET", "https://example.com/", map[string]string{}, nil)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if resp.Status != 200 || string(resp.Body) != "coal-body" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestCoalescer_Submit_Stopped(t *testing.T) {
	c := NewCoalescer(httpDefaultClient(), []string{"http://127.0.0.1/"}, "h", "k", time.Second)
	c.Stop()
	_, err := c.Submit("GET", "https://example.com", nil, nil)
	if err == nil || err.Error() != "proxy stopped" {
		t.Fatalf("err = %v", err)
	}
}

func httpDefaultClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Second}
}

func TestTryOneURL_Exported(t *testing.T) {
	srv := mockAppsScriptServer(t, 200, "one")
	defer srv.Close()
	payload := BuildRelayPayload("k", "GET", "https://x.com", nil, nil)
	resp, err := TryOneURL(context.Background(), srv.Client(), srv.URL, srvHost(srv), payload, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "one" {
		t.Fatalf("body = %q", resp.Body)
	}
}

func TestAppsScriptRoundTrip_Exported(t *testing.T) {
	srv := mockAppsScriptServer(t, 200, "rt")
	defer srv.Close()
	payload := BuildRelayPayload("k", "GET", "https://x.com", nil, nil)
	raw, err := AppsScriptRoundTrip(context.Background(), srv.Client(), srv.URL, srvHost(srv), payload, 5*time.Second)
	if err != nil || len(raw) == 0 {
		t.Fatalf("round trip: %v len=%d", err, len(raw))
	}
}
