package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Vektor uji resmi AWS ("GET Object" pada dokumentasi Signature Version 4
// untuk Amazon S3).
func TestSignV4AWSVector(t *testing.T) {
	when := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	emptyHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	auth, amzDate := signV4("GET", "/test.txt", "", map[string]string{
		"host":                 "examplebucket.s3.amazonaws.com",
		"range":                "bytes=0-9",
		"x-amz-content-sha256": emptyHash,
	}, emptyHash, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", "s3", when)
	if amzDate != "20130524T000000Z" {
		t.Fatalf("amz date %s", amzDate)
	}
	want := "Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if !strings.HasSuffix(auth, want) {
		t.Fatalf("signature mismatch:\n got %s\nwant ...%s", auth, want)
	}
	if !strings.Contains(auth, "SignedHeaders=host;range;x-amz-content-sha256;x-amz-date") {
		t.Fatalf("signed headers: %s", auth)
	}
}

func TestURIEncode(t *testing.T) {
	if got := uriEncode("scans/2026/10/26/a b+c.jpg", true); got != "scans/2026/10/26/a%20b%2Bc.jpg" {
		t.Fatal(got)
	}
	if got := uriEncode("a/b", false); got != "a%2Fb" {
		t.Fatal(got)
	}
}

// fakeS3 menyimpan objek di memori dan memeriksa bentuk request.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") ||
		r.Header.Get("X-Amz-Date") == "" || r.Header.Get("X-Amz-Content-Sha256") == "" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.EscapedPath()
	if path == "/faces" && r.Method == http.MethodHead {
		return
	}
	key := strings.TrimPrefix(path, "/faces/")
	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		if sha256Hex(body) != r.Header.Get("X-Amz-Content-Sha256") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.objects[key] = body
		f.types[key] = r.Header.Get("Content-Type")
	case http.MethodGet:
		body, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", f.types[key])
		_, _ = w.Write(body)
	case http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestS3RoundTrip(t *testing.T) {
	fake := &fakeS3{objects: map[string][]byte{}, types: map[string]string{}}
	server := httptest.NewServer(fake)
	defer server.Close()
	client, err := NewS3(Config{Endpoint: server.URL, Region: "garage", Bucket: "faces", AccessKeyID: "AK", SecretAccessKey: "SK"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.HeadBucket(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Put(ctx, "scans/2026/10/26/x.jpg", "image/jpeg", []byte("jpeg")); err != nil {
		t.Fatal(err)
	}
	body, contentType, _, err := client.Get(ctx, "scans/2026/10/26/x.jpg")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(body)
	body.Close()
	if string(data) != "jpeg" || contentType != "image/jpeg" {
		t.Fatalf("got %q %q", data, contentType)
	}
	if err := client.Delete(ctx, "scans/2026/10/26/x.jpg"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := client.Get(ctx, "scans/2026/10/26/x.jpg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestNewS3RequiresConfig(t *testing.T) {
	if _, err := NewS3(Config{Endpoint: "http://x:3900", Bucket: "b"}); err == nil {
		t.Fatal("missing keys must fail")
	}
	if _, err := NewS3(Config{Endpoint: "ftp://x", Bucket: "b", AccessKeyID: "a", SecretAccessKey: "s"}); err == nil {
		t.Fatal("bad scheme must fail")
	}
}
