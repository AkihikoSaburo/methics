package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

type failingStore struct{ puts int }

func (f *failingStore) Put(context.Context, string, string, []byte) error {
	f.puts++
	return errors.New("garage down")
}
func (f *failingStore) Get(context.Context, string) (io.ReadCloser, string, int64, error) {
	return nil, "", 0, errors.New("garage down")
}
func (f *failingStore) Delete(context.Context, string) error { return nil }
func (f *failingStore) HeadBucket(context.Context) error     { return errors.New("garage down") }

func TestImageKeys(t *testing.T) {
	at := time.Date(2026, 10, 26, 5, 4, 3, 0, time.UTC)
	if key := scanImageKey("ESP32 GED/A1", at, "jpg"); !regexp.MustCompile(`^scans/2026/10/26/050403Z-ESP32_GED_A1-[0-9a-f]{12}\.jpg$`).MatchString(key) {
		t.Fatalf("scan key %q", key)
	}
	if key := enrollImageKey("student-ik24001", at, "png"); !regexp.MustCompile(`^enroll/student-ik24001/20261026T050403Z-[0-9a-f]{12}\.png$`).MatchString(key) {
		t.Fatalf("enroll key %q", key)
	}
}

func TestImageExtension(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0000")
	if ext, ct := imageExtension(png); ext != "png" || ct != "image/png" {
		t.Fatal(ext, ct)
	}
	if ext, ct := imageExtension([]byte{0xFF, 0xD8, 0xFF, 0xE0}); ext != "jpg" || ct != "image/jpeg" {
		t.Fatal(ext, ct)
	}
}

func TestUploadFailureDoesNotBlock(t *testing.T) {
	store := &failingStore{}
	s := New(nil, Config{})
	s.SetImageStore(store)
	if key := s.uploadImage(context.Background(), "scans/x.jpg", "image/jpeg", []byte("x")); key != "" || store.puts != 1 {
		t.Fatalf("got %q puts=%d", key, store.puts)
	}
	if health := s.imageStorageHealth(context.Background()); health["enabled"] != true || health["online"] != false {
		t.Fatalf("health %+v", health)
	}
}

func TestImageStorageDisabled(t *testing.T) {
	s := New(nil, Config{})
	if s.uploadImage(context.Background(), "k", "image/jpeg", []byte("x")) != "" {
		t.Fatal("disabled storage must not upload")
	}
	if removed, err := s.PurgeExpiredImages(context.Background()); removed != 0 || err != nil {
		t.Fatal(removed, err)
	}
	if ImageURL("") != "" || ImageURL("scans/a.jpg") != "/api/v1/images/scans/a.jpg" {
		t.Fatal("ImageURL")
	}
	// Tanpa sesi staf, endpoint foto menolak.
	recorder := httptest.NewRecorder()
	s.serveImage(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/images/scans/a.jpg", nil))
	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), "unauthorized") {
		t.Fatalf("got %d %s", recorder.Code, recorder.Body.String())
	}
}
