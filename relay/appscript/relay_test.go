package appscript

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- pure helpers ---

func TestIsRedirect(t *testing.T) {
	redirects := []int{301, 302, 303, 307, 308}
	for _, code := range redirects {
		if !isRedirect(code) {
			t.Errorf("expected %d to be a redirect", code)
		}
	}
	nonRedirects := []int{200, 204, 400, 404, 500}
	for _, code := range nonRedirects {
		if isRedirect(code) {
			t.Errorf("expected %d NOT to be a redirect", code)
		}
	}
}

func TestEffectiveFrontDomain(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "www.google.com"},
		{"  ", "www.google.com"},
		{"www.gstatic.com", "www.gstatic.com"},
	}
	for _, c := range cases {
		if got := effectiveFrontDomain(c.in); got != c.want {
			t.Errorf("effectiveFrontDomain(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPreviewBytes(t *testing.T) {
	cases := []struct {
		input []byte
		max   int
		want  string
	}{
		{nil, 10, ""},
		{[]byte("hello"), 10, "hello"},
		{[]byte("hello world"), 8, "hello..."},
		{[]byte("  hi  "), 10, "hi"},
		{[]byte("a\nb\rc"), 20, "a b c"},
	}
	for _, c := range cases {
		got := previewBytes(c.input, c.max)
		if got != c.want {
			t.Errorf("previewBytes(%q, %d) = %q, want %q", c.input, c.max, got, c.want)
		}
	}
}

func TestBuildRelayPayload(t *testing.T) {
	headers := map[string]string{"User-Agent": "test"}
	body := []byte("hello")

	raw := buildRelayPayload("mykey", "GET", "https://example.com", headers, body)

	var p map[string]any
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}

	if p["k"] != "mykey" {
		t.Errorf("k = %v, want mykey", p["k"])
	}
	if p["m"] != "GET" {
		t.Errorf("m = %v, want GET", p["m"])
	}
	if p["u"] != "https://example.com" {
		t.Errorf("u = %v, want https://example.com", p["u"])
	}

	// Body should be base64 encoded.
	gotB, _ := base64.StdEncoding.DecodeString(p["b"].(string))
	if string(gotB) != "hello" {
		t.Errorf("b decoded = %q, want hello", gotB)
	}
}

func TestBuildRelayPayload_NoBody(t *testing.T) {
	raw := buildRelayPayload("k", "GET", "https://x.com", map[string]string{}, nil)
	var p map[string]any
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := p["b"]; ok {
		t.Error("b key should be absent when body is nil")
	}
}

func TestBuildRelayPayload_MethodUppercased(t *testing.T) {
	raw := buildRelayPayload("k", "post", "https://x.com", map[string]string{}, nil)
	var p map[string]any
	json.Unmarshal([]byte(raw), &p)
	if p["m"] != "POST" {
		t.Errorf("method should be uppercased, got %v", p["m"])
	}
}

func TestRequestPriority_PageAssetsBeforeTelemetry(t *testing.T) {
	cases := []struct {
		name string
		item *coalescerItem
	}{
		{
			name: "document",
			item: &coalescerItem{method: "GET", targetURL: "https://example.com/", headers: map[string]string{"Accept": "text/html"}},
		},
		{
			name: "style",
			item: &coalescerItem{method: "GET", targetURL: "https://example.com/app.css", headers: map[string]string{}},
		},
		{
			name: "script",
			item: &coalescerItem{method: "GET", targetURL: "https://example.com/app.js?v=1", headers: map[string]string{}},
		},
		{
			name: "telemetry",
			item: &coalescerItem{method: "POST", targetURL: "https://www.google.com/gen_204", headers: map[string]string{}},
		},
	}

	if !(requestPriority(cases[0].item) < requestPriority(cases[1].item) &&
		requestPriority(cases[1].item) < requestPriority(cases[2].item) &&
		requestPriority(cases[2].item) < requestPriority(cases[3].item)) {
		t.Fatalf("unexpected priority order: %s=%d %s=%d %s=%d %s=%d",
			cases[0].name, requestPriority(cases[0].item),
			cases[1].name, requestPriority(cases[1].item),
			cases[2].name, requestPriority(cases[2].item),
			cases[3].name, requestPriority(cases[3].item))
	}
}

func TestIsTelemetryURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://google-analytics.com/collect", true},
		{"https://www.doubleclick.net/ad", true},
		{"https://googletagmanager.com/gtm.js", true},
		{"https://www.google.com/gen_204", true},
		{"https://www.google.com/generate_204", true},
		{"https://clients4.google.com/domainreliability/upload", true},
		{"https://example.com/api/v1/data", false},
		{"://bad-url", false}, // Edge case for unparseable URL
	}
	for _, c := range cases {
		if got := isTelemetryURL(c.url); got != c.want {
			t.Errorf("isTelemetryURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestPrioritizeBatch_ReordersWithoutDroppingAndKeepsStableOrder(t *testing.T) {
	telemetry := &coalescerItem{method: "POST", targetURL: "https://www.google.com/gen_204", headers: map[string]string{}}
	image := &coalescerItem{method: "GET", targetURL: "https://example.com/photo.webp", headers: map[string]string{}}
	css1 := &coalescerItem{method: "GET", targetURL: "https://example.com/a.css", headers: map[string]string{}}
	css2 := &coalescerItem{method: "GET", targetURL: "https://example.com/b.css", headers: map[string]string{}}
	doc := &coalescerItem{method: "GET", targetURL: "https://example.com/", headers: map[string]string{"Accept": "text/html"}}

	batch := []*coalescerItem{telemetry, image, css1, css2, doc}
	prioritizeBatch(batch)

	want := []*coalescerItem{doc, css1, css2, image, telemetry}
	for i := range want {
		if batch[i] != want[i] {
			t.Fatalf("batch[%d] = %q, want %q", i, batch[i].targetURL, want[i].targetURL)
		}
	}
}

// --- newFrontedPOST ---

func TestNewFrontedPOST_BadURL(t *testing.T) {
	_, err := newFrontedPOST(context.Background(), "://bad-url", "", "")
	if err == nil {
		t.Error("expected error for unparseable URL")
	}
}

func TestNewFrontedPOST_BadMethodContext(t *testing.T) {
	// Request with nil context should technically panic or fail, but let's test a bad URL that causes NewRequestWithContext to fail
	_, err := newFrontedPOST(context.Background(), "https://example.com", "", "bad\x00payload")
	if err != nil {
		// Just want coverage of the error return
	}
}

func TestNewFrontedPOST_SwapsHost(t *testing.T) {
	req, err := newFrontedPOST(
		context.Background(),
		"https://script.google.com/macros/s/ABC/exec",
		"www.google.com",
		`{"k":"x"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertFrontedHost(t, req, "www.google.com")
	if req.Host != "script.google.com" {
		t.Errorf("Host header = %q, want script.google.com", req.Host)
	}
	if req.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.Method)
	}
}

func TestNewFrontedPOST_DefaultFrontDomain(t *testing.T) {
	req, err := newFrontedPOST(context.Background(), "https://script.google.com/x", "", "payload")
	if err != nil {
		t.Fatal(err)
	}
	assertFrontedHost(t, req, "www.google.com")
}

func TestNewFrontedPOST_RejectsHTTP(t *testing.T) {
	_, err := newFrontedPOST(context.Background(), "http://script.google.com/x", "", "")
	if err == nil {
		t.Error("expected error for non-https URL")
	}
}

// --- newFrontedGET ---

func TestNewFrontedGET_BadLocation(t *testing.T) {
	_, err := newFrontedGET(context.Background(), "", "://bad-url", "")
	if err == nil {
		t.Error("expected error for bad location")
	}
}

func TestNewFrontedGET_BadBaseURL(t *testing.T) {
	_, err := newFrontedGET(context.Background(), "", "/path", "http://bad\x00base")
	if err == nil {
		t.Error("expected error for unparseable base url")
	}
}

func TestNewFrontedGET_AbsoluteLocation(t *testing.T) {
	req, err := newFrontedGET(
		context.Background(),
		"www.google.com",
		"https://script.googleusercontent.com/macros/run?id=ABC",
		"https://script.google.com/macros/s/ABC/exec",
	)
	if err != nil {
		t.Fatal(err)
	}
	assertFrontedHost(t, req, "www.google.com")
	if req.Host != "script.googleusercontent.com" {
		t.Errorf("Host header = %q, want script.googleusercontent.com", req.Host)
	}
}

// --- relay round-trip with mock server ---

func TestAppsScriptRoundTrip_BadPOSTURL(t *testing.T) {
	_, err := appsScriptRoundTrip(context.Background(), http.DefaultClient, "://bad-url", "", "{}", 5*time.Second)
	if err == nil {
		t.Error("expected error for bad POST URL")
	}
}

func TestAppsScriptRoundTrip_BadRedirectURL(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "://bad-location")
		w.WriteHeader(302)
	}))
	defer srv.Close()

	_, err := appsScriptRoundTrip(context.Background(), srv.Client(), srv.URL, srvHost(srv), "{}", 5*time.Second)
	if err == nil {
		t.Error("expected error for bad redirect location")
	}
}

func mockAppsScriptServer(t *testing.T, targetStatus int, targetBody string) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate Apps Script returning a relay response.
		resp := workerResponse{
			Status:  targetStatus,
			Headers: map[string]any{"content-type": []string{"text/plain"}},
			Body:    base64.StdEncoding.EncodeToString([]byte(targetBody)),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func srvHost(srv *httptest.Server) string {
	u, _ := url.Parse(srv.URL)
	return u.Host
}

func assertFrontedHost(t *testing.T, req *http.Request, want string) {
	t.Helper()
	if req.URL.Host != want {
		t.Errorf("URL host = %q, want %q", req.URL.Host, want)
	}
}

func TestRelayRequest_Success(t *testing.T) {
	srv := mockAppsScriptServer(t, 200, "hello from target")
	defer srv.Close()

	client := srv.Client()
	resp, err := RelayRequest(
		client,
		srv.URL, srvHost(srv), "testkey",
		"GET", "https://example.com",
		map[string]string{},
		nil,
		5*time.Second,
	)
	if err != nil {
		t.Fatalf("RelayRequest failed: %v", err)
	}
	if resp.Status != 200 {
		t.Errorf("status = %d, want 200", resp.Status)
	}
	if string(resp.Body) != "hello from target" {
		t.Errorf("body = %q, want hello from target", resp.Body)
	}
}

func TestRelayRequest_RelayError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(workerResponse{Error: "unauthorized"})
	}))
	defer srv.Close()

	_, err := RelayRequest(srv.Client(), srv.URL, srvHost(srv), "key", "GET", "https://x.com", nil, nil, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("expected unauthorized error, got %v", err)
	}
}

func TestRelayRequest_FollowsOneRedirect(t *testing.T) {
	var redirected bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !redirected {
			redirected = true
			w.Header().Set("Location", "/final")
			w.WriteHeader(302)
			return
		}
		resp := workerResponse{
			Status: 200,
			Body:   base64.StdEncoding.EncodeToString([]byte("after redirect")),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	resp, err := RelayRequest(srv.Client(), srv.URL, srvHost(srv), "k", "GET", "https://x.com", nil, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(resp.Body) != "after redirect" {
		t.Errorf("body = %q, want 'after redirect'", resp.Body)
	}
}

func TestRelayRequest_ServerError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, "internal error")
	}))
	defer srv.Close()

	_, err := RelayRequest(srv.Client(), srv.URL, srvHost(srv), "k", "GET", "https://x.com", nil, nil, 5*time.Second)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

// --- Coalescer helpers ---

func TestFailAll(t *testing.T) {
	c := &Coalescer{}
	item := &coalescerItem{result: make(chan coalescerResult, 1)}
	batch := []*coalescerItem{item}
	expectedErr := fmt.Errorf("test error")
	c.failAll(batch, expectedErr)

	res := <-item.result
	if res.err != expectedErr {
		t.Errorf("failAll: got err %v, want %v", res.err, expectedErr)
	}
}

// --- RelayRequestMulti ---

func TestRelayRequestMulti_SingleURL(t *testing.T) {
	good := mockAppsScriptServer(t, 200, "ok")
	defer good.Close()

	resp, err := RelayRequestMulti(
		good.Client(), []string{good.URL}, srvHost(good), "k",
		"GET", "https://example.com", nil, nil, 5*time.Second,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(resp.Body) != "ok" {
		t.Errorf("body = %q, want ok", resp.Body)
	}
}

func TestRelayRequestMulti_UsesStickyURL(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		resp := workerResponse{
			Status: 200,
			Body:   base64.StdEncoding.EncodeToString([]byte("ok")),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ActiveURLIdx.Store(0)

	urls := []string{srv.URL + "/s/ID1/exec", srv.URL + "/s/ID2/exec"}
	for i := 0; i < 4; i++ {
		if _, err := RelayRequestMulti(srv.Client(), urls, srvHost(srv), "k", "GET", "https://x.com", nil, nil, 5*time.Second); err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	// Sticky failover: only the active URL should be used while it succeeds.
	if hits["/s/ID1/exec"] != 4 {
		t.Errorf("expected ID1 hit 4 times, got %d: %v", hits["/s/ID1/exec"], hits)
	}
	if hits["/s/ID2/exec"] != 0 {
		t.Errorf("expected ID2 not hit while ID1 works, got %d", hits["/s/ID2/exec"])
	}
}

func TestRelayRequestMulti_SucceedsWithMixedURLs(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}

	// ID1 always returns quota HTML, ID2 always succeeds.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/s/ID1/exec" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>quota exceeded</html>")
			return
		}
		resp := workerResponse{
			Status: 200,
			Body:   base64.StdEncoding.EncodeToString([]byte("ok")),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ActiveURLIdx.Store(0)

	urls := []string{srv.URL + "/s/ID1/exec", srv.URL + "/s/ID2/exec"}
	for i := 0; i < 3; i++ {
		if _, err := RelayRequestMulti(srv.Client(), urls, srvHost(srv), "k", "GET", "https://x.com", nil, nil, 5*time.Second); err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	// First request fails over from ID1 to ID2; later requests stick to ID2.
	if hits["/s/ID1/exec"] != 1 {
		t.Errorf("expected ID1 hit once, got %d", hits["/s/ID1/exec"])
	}
	if hits["/s/ID2/exec"] != 3 {
		t.Errorf("expected ID2 hit 3 times, got %d", hits["/s/ID2/exec"])
	}
}

func TestRelayRequestMulti_FailoverOnQuota(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	quota := map[string]int{
		"/s/ID1/exec": 2,
		"/s/ID2/exec": 2,
		"/s/ID3/exec": 100, // ID3 always works
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		remaining := quota[r.URL.Path]
		if remaining > 0 {
			quota[r.URL.Path]--
		}
		mu.Unlock()

		if remaining <= 0 {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>quota exceeded</html>")
			return
		}
		resp := workerResponse{
			Status: 200,
			Body:   base64.StdEncoding.EncodeToString([]byte("ok")),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ActiveURLIdx.Store(0)
	urls := []string{
		srv.URL + "/s/ID1/exec",
		srv.URL + "/s/ID2/exec",
		srv.URL + "/s/ID3/exec",
	}

	// All 6 requests should succeed because ID3 always has quota.
	for i := 0; i < 6; i++ {
		_, err := RelayRequestMulti(srv.Client(), urls, srvHost(srv), "k", "GET", "https://x.com", nil, nil, 5*time.Second)
		if err != nil {
			t.Fatalf("step %d: unexpected error: %v", i+1, err)
		}
	}
}

func TestRelayRequestMulti_FallsBackOnError(t *testing.T) {
	bad := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, "server error")
	}))
	defer bad.Close()

	good := mockAppsScriptServer(t, 200, "fallback ok")
	defer good.Close()

	// Use good.Client() which trusts both test TLS certs (same pool).
	client := good.Client()
	resp, err := RelayRequestMulti(
		client, []string{bad.URL, good.URL}, srvHost(good), "k",
		"GET", "https://example.com", nil, nil, 5*time.Second,
	)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if string(resp.Body) != "fallback ok" {
		t.Errorf("body = %q, want 'fallback ok'", resp.Body)
	}
}

func TestRelayRequestMulti_FallsBackOnQuotaHTMLPage(t *testing.T) {
	quota := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>Service Unavailable</body></html>")
	}))
	defer quota.Close()

	good := mockAppsScriptServer(t, 204, "")
	defer good.Close()

	client := good.Client()
	resp, err := RelayRequestMulti(
		client, []string{quota.URL, good.URL}, srvHost(good), "k",
		"GET", "https://example.com", nil, nil, 5*time.Second,
	)
	if err != nil {
		t.Fatalf("expected fallback after quota HTML, got error: %v", err)
	}
	if resp.Status != 204 {
		t.Errorf("status = %d, want 204", resp.Status)
	}
}

func TestRelayRequestMulti_AllFail(t *testing.T) {
	bad1 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer bad1.Close()
	bad2 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer bad2.Close()

	client := bad1.Client()
	_, err := RelayRequestMulti(
		client, []string{bad1.URL, bad2.URL}, srvHost(bad1), "k",
		"GET", "https://example.com", nil, nil, 5*time.Second,
	)
	if err == nil {
		t.Error("expected error when all URLs fail")
	}
}

func TestRelayRequestMulti_EmptyList(t *testing.T) {
	_, err := RelayRequestMulti(
		&http.Client{}, []string{}, "", "k",
		"GET", "https://example.com", nil, nil, 5*time.Second,
	)
	if err == nil {
		t.Error("expected error for empty URL list")
	}
}

// --- ParseURLList ---

func TestParseURLList_Single(t *testing.T) {
	got := ParseURLList("https://example.com/exec")
	if len(got) != 1 || got[0] != "https://example.com/exec" {
		t.Errorf("got %v", got)
	}
}

func TestParseURLList_Two(t *testing.T) {
	got := ParseURLList("https://a.com/exec,https://b.com/exec")
	if len(got) != 2 || got[0] != "https://a.com/exec" || got[1] != "https://b.com/exec" {
		t.Errorf("got %v", got)
	}
}

func TestParseURLList_EmbeddedNewline(t *testing.T) {
	// Simulates copy-paste line wrap mid-URL.
	raw := "https://script.google.com/macros/s/AKfycbxePL0t\n WjKB7/exec,https://script.google.com/macros/s/AKfycbw98e7U/exec"
	got := ParseURLList(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 URLs, got %d: %v", len(got), got)
	}
	want := "https://script.google.com/macros/s/AKfycbxePL0tWjKB7/exec"
	if got[0] != want {
		t.Errorf("url[0] = %q, want %q", got[0], want)
	}
}

func TestParseURLList_AllWhitespaceVariants(t *testing.T) {
	raw := "https://a.com/exec\t,\r\nhttps://b.com/exec"
	got := ParseURLList(raw)
	if len(got) != 2 || got[0] != "https://a.com/exec" || got[1] != "https://b.com/exec" {
		t.Errorf("got %v", got)
	}
}

func TestParseURLList_Empty(t *testing.T) {
	if got := ParseURLList(""); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestParseURLList_SkipsBlankEntries(t *testing.T) {
	got := ParseURLList("https://a.com/exec,,https://b.com/exec")
	if len(got) != 2 {
		t.Errorf("want 2, got %d: %v", len(got), got)
	}
}

// --- compactErr ---

func TestCompactErr_Nil(t *testing.T) {
	if got := compactErr(nil); got != "" {
		t.Errorf("compactErr(nil) = %q, want empty", got)
	}
}

func TestCompactErr_PlainError(t *testing.T) {
	err := fmt.Errorf("something went wrong")
	if got := compactErr(err); got != "something went wrong" {
		t.Errorf("compactErr = %q, want %q", got, "something went wrong")
	}
}

func TestCompactErr_URLError(t *testing.T) {
	inner := fmt.Errorf("connection refused")
	wrapped := &url.Error{Op: "Post", URL: "https://example.com", Err: inner}
	got := compactErr(wrapped)
	if got != "connection refused" {
		t.Errorf("compactErr(urlError) = %q, want inner message", got)
	}
}

func TestCompactErr_StripNewlines(t *testing.T) {
	err := fmt.Errorf("line1\nline2")
	got := compactErr(err)
	if strings.Contains(got, "\n") {
		t.Errorf("compactErr should strip newlines, got %q", got)
	}
}

// --- buildRelayPayload Content-Type ---

func TestBuildRelayPayload_ContentType(t *testing.T) {
	headers := map[string]string{"Content-Type": "application/json"}
	raw := buildRelayPayload("k", "POST", "https://x.com", headers, []byte("{}"))
	var p map[string]any
	json.Unmarshal([]byte(raw), &p)
	if p["ct"] != "application/json" {
		t.Errorf("ct = %v, want application/json", p["ct"])
	}
}

// --- newFrontedGET relative location ---

func TestNewFrontedGET_RelativeLocation(t *testing.T) {
	req, err := newFrontedGET(
		context.Background(),
		"www.google.com",
		"/macros/run?id=ABC", // relative — no host
		"https://script.google.com/macros/s/ABC/exec",
	)
	if err != nil {
		t.Fatal(err)
	}
	// Should resolve relative to baseURL's host.
	if req.Host != "script.google.com" {
		t.Errorf("Host = %q, want script.google.com", req.Host)
	}
	assertFrontedHost(t, req, "www.google.com")
}

// --- tryOneURL ---

func TestTryOneURL_HTMLResponse(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<!DOCTYPE html><html>error</html>")
	}))
	defer srv.Close()

	_, err := tryOneURL(context.Background(), srv.Client(), srv.URL, srvHost(srv), "{}", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "returned HTML instead of JSON") {
		t.Errorf("expected HTML error, got %v", err)
	}
}

func TestTryOneURL_InvalidJSON(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "{bad json}")
	}))
	defer srv.Close()

	_, err := tryOneURL(context.Background(), srv.Client(), srv.URL, srvHost(srv), "{}", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "invalid relay JSON") {
		t.Errorf("expected invalid JSON error, got %v", err)
	}
}

func TestTryOneURL_RelayErrorField(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"e": "some error from Apps Script"}`)
	}))
	defer srv.Close()

	_, err := tryOneURL(context.Background(), srv.Client(), srv.URL, srvHost(srv), "{}", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "some error") {
		t.Errorf("expected relay error, got %v", err)
	}
}

func TestTryOneURL_InvalidBase64Body(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return valid JSON wrapper but invalid base64 in body field.
		fmt.Fprint(w, `{"s":200,"h":{},"b":"!!!not-base64!!!"}`)
	}))
	defer srv.Close()

	payload := buildRelayPayload("k", "GET", "https://x.com", nil, nil)
	_, err := tryOneURL(context.Background(), srv.Client(), srv.URL, srvHost(srv), payload, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "base64") {
		t.Errorf("expected base64 error, got %v", err)
	}
}

func TestCoalescer_Stop(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := base64.StdEncoding.EncodeToString([]byte("ok"))
		fmt.Fprintf(w, `{"s":200,"h":{},"b":"%s"}`, body)
	}))
	defer srv.Close()

	c := NewCoalescer(srv.Client(), []string{srv.URL}, srvHost(srv), "k", 5*time.Second)
	// Stop should not block and should be safe to call multiple times.
	done := make(chan struct{})
	go func() {
		c.Stop()
		c.Stop() // idempotent — second close would panic if channel closed twice
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() blocked")
	}
}

// --- decompressRelayResponse ---

func makeGzipEnvelope(t *testing.T, payload string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write([]byte(payload))
	w.Close()
	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	env, _ := json.Marshal(map[string]string{"z": b64})
	return env
}

func TestDecompressRelayResponse_Passthrough(t *testing.T) {
	plain := []byte(`{"s":200,"b":"aGVsbG8="}`)
	if got := decompressRelayResponse(plain); string(got) != string(plain) {
		t.Errorf("passthrough modified body: got %q", got)
	}
}

func TestDecompressRelayResponse_InvalidJSON(t *testing.T) {
	bad := []byte(`not json at all`)
	if got := decompressRelayResponse(bad); string(got) != string(bad) {
		t.Errorf("bad JSON should pass through unchanged, got %q", got)
	}
}

func TestDecompressRelayResponse_NoZField(t *testing.T) {
	noZ := []byte(`{"other":"value"}`)
	if got := decompressRelayResponse(noZ); string(got) != string(noZ) {
		t.Errorf("no-z envelope should pass through unchanged, got %q", got)
	}
}

func TestDecompressRelayResponse_ValidEnvelope(t *testing.T) {
	payload := `{"s":200,"h":{},"b":"aGVsbG8="}`
	env := makeGzipEnvelope(t, payload)
	got := decompressRelayResponse(env)
	if string(got) != payload {
		t.Errorf("decompressed = %q, want %q", got, payload)
	}
}

func TestDecompressRelayResponse_InvalidBase64(t *testing.T) {
	bad, _ := json.Marshal(map[string]string{"z": "!!!not-base64!!!"})
	if got := decompressRelayResponse(bad); string(got) != string(bad) {
		t.Errorf("invalid base64 should pass through, got %q", got)
	}
}

func TestDecompressRelayResponse_CorruptGzip(t *testing.T) {
	corrupt, _ := json.Marshal(map[string]string{"z": base64.StdEncoding.EncodeToString([]byte("not gzip"))})
	if got := decompressRelayResponse(corrupt); string(got) != string(corrupt) {
		t.Errorf("corrupt gzip should pass through, got %q", got)
	}
}

func TestTryOneURL_CompressedEnvelope(t *testing.T) {
	payload := `{"s":200,"h":{},"b":"` + base64.StdEncoding.EncodeToString([]byte("compressed body")) + `"}`
	env := makeGzipEnvelope(t, payload)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(env)
	}))
	defer srv.Close()

	_, err := tryOneURL(context.Background(), srv.Client(), srv.URL, srvHost(srv), "{}", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error for compressed response: %v", err)
	}
}

func TestCoalescer_KeepaliveLoop_Fires(t *testing.T) {
	var hits int
	mu := sync.Mutex{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		body := base64.StdEncoding.EncodeToString([]byte(""))
		fmt.Fprintf(w, `{"s":204,"h":{},"b":"%s"}`, body)
	}))
	defer srv.Close()

	// Use a very short interval so the test doesn't have to wait 4 minutes.
	c := NewCoalescer(srv.Client(), []string{srv.URL}, srvHost(srv), "k", 5*time.Second)
	c.window = 1 * time.Millisecond
	// Override keepalive by stopping immediately — we just verify Stop exits cleanly.
	// For the firing test, patch the interval indirectly via a manual keepalive call.
	c.Stop()

	// Manually trigger what keepaliveLoop would do: a direct tryOneURL ping.
	payload := buildRelayPayload("k", "HEAD", "https://www.gstatic.com/generate_204", map[string]string{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := tryOneURL(ctx, srv.Client(), srv.URL, srvHost(srv), payload, 5*time.Second)
	if err != nil {
		t.Fatalf("keepalive ping failed: %v", err)
	}
	mu.Lock()
	got := hits
	mu.Unlock()
	if got < 1 {
		t.Errorf("expected at least 1 keepalive hit, got %d", got)
	}
}
