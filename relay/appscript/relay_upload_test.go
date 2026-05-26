package appscript

import (
	"testing"
	"time"
)

func TestIsUploadRequest(t *testing.T) {
	cases := []struct {
		name string
		item *coalescerItem
		want bool
	}{
		{
			name: "multipart post",
			item: &coalescerItem{
				method: "POST",
				targetURL: "https://www.youtube.com/upload",
				headers: map[string]string{"Content-Type": "multipart/form-data; boundary=x"},
			},
			want: true,
		},
		{
			name: "googlevideo",
			item: &coalescerItem{
				method:    "POST",
				targetURL: "https://rr3---sn-ab5s.googlevideo.com/videoplayback",
			},
			want: true,
		},
		{
			name: "drive",
			item: &coalescerItem{
				method:    "POST",
				targetURL: "https://drive.google.com/upload/resumable",
			},
			want: true,
		},
		{
			name: "get css",
			item: &coalescerItem{
				method:    "GET",
				targetURL: "https://example.com/app.css",
			},
			want: false,
		},
		{
			name: "post telemetry",
			item: &coalescerItem{
				method:    "POST",
				targetURL: "https://www.google.com/gen_204",
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUploadRequest(tc.item); got != tc.want {
				t.Fatalf("isUploadRequest() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRequestPriority_UploadBeforeAssets(t *testing.T) {
	upload := &coalescerItem{
		method:    "POST",
		targetURL: "https://rr3---sn-ab5s.googlevideo.com/videoplayback",
		headers:   map[string]string{"Content-Type": "multipart/form-data"},
	}
	image := &coalescerItem{method: "GET", targetURL: "https://example.com/photo.webp", headers: map[string]string{}}
	telemetry := &coalescerItem{method: "POST", targetURL: "https://www.google.com/gen_204", headers: map[string]string{}}

	if !(requestPriority(upload) < requestPriority(image) &&
		requestPriority(image) < requestPriority(telemetry)) {
		t.Fatalf("upload=%d image=%d telemetry=%d",
			requestPriority(upload), requestPriority(image), requestPriority(telemetry))
	}
}

func TestBypassesBatch(t *testing.T) {
	small := &coalescerItem{body: make([]byte, largeUploadBypassBatch-1)}
	large := &coalescerItem{body: make([]byte, largeUploadBypassBatch)}
	if bypassesBatch(small) {
		t.Fatal("small body should not bypass batch")
	}
	if !bypassesBatch(large) {
		t.Fatal("large body should bypass batch")
	}
}

func TestRelayTimeoutForBody(t *testing.T) {
	base := 45 * time.Second
	if got := relayTimeoutForBody(base, 0); got != base {
		t.Fatalf("empty body = %v, want %v", got, base)
	}
	got := relayTimeoutForBody(base, 20*1024*1024)
	want := base + 10*time.Second
	if got != want {
		t.Fatalf("20MB body timeout = %v, want %v", got, want)
	}
	huge := relayTimeoutForBody(base, 2*1024*1024*1024)
	if huge != maxRelayTimeout {
		t.Fatalf("expected cap at %v, got %v", maxRelayTimeout, huge)
	}
}

func TestBatchRelayTimeout_UsesLargestBody(t *testing.T) {
	base := 30 * time.Second
	batch := []*coalescerItem{
		{body: make([]byte, 1024)},
		{body: make([]byte, 15 * 1024 * 1024)},
	}
	got := batchRelayTimeout(base, batch)
	want := relayTimeoutForBody(base, 15*1024*1024)
	if got != want {
		t.Fatalf("batch timeout = %v, want %v", got, want)
	}
}
