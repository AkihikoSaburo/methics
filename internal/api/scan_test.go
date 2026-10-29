package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sequenceFace meniru POST /embed: panggilan ke-i mengembalikan responses[i]
// (embedding, atau nil = 422 "No face detected").
func sequenceFace(t *testing.T, responses [][]float64, status int) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(atomic.AddInt32(&calls, 1)) - 1
		if status != 0 {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"detail": "Face Recognition models are not initialized."})
			return
		}
		if i >= len(responses) || responses[i] == nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{"detail": "No face detected in the uploaded image"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": responses[i], "confidence": 0.9, "processing_time_ms": 5})
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

var scanTemplates = []faceTemplate{
	{StudentID: "student-241011001", Encodings: [][]float64{unit(1, 0, 0), unit(1, 0.3, 0)}},
	{StudentID: "student-241011002", Encodings: [][]float64{unit(0, 1, 0)}},
}

func frames(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte{byte(i + 1)}
	}
	return out
}

func TestMatchFramesStopsAfterEnoughMatches(t *testing.T) {
	service, calls := sequenceFace(t, [][]float64{nil, unit(1, 0.05, 0), unit(1, 0.1, 0), unit(1, 0, 0)}, 0)
	s := New(nil, Config{FaceServiceURL: service.URL})
	results, err := s.matchFrames(context.Background(), frames(4), scanTemplates, 2)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 3 || len(results) != 3 {
		t.Fatalf("should stop after 2 matching frames: calls=%d results=%d", *calls, len(results))
	}
	verdict := decideFrames(results, 4, 0.45)
	if verdict.StudentID != "student-241011001" || verdict.FramesMatched != 2 || verdict.FramesWithFace != 2 {
		t.Fatalf("unexpected verdict %+v", verdict)
	}
	status, result, _ := scanStatus(verdict, true, false, 2)
	if status != "Hadir" || result != ScanResultPresent {
		t.Fatalf("two consistent frames must be Hadir, got %s/%s", status, result)
	}
}

func TestMatchFramesUnknownFace(t *testing.T) {
	stranger := unit(0, 0, 1)
	service, calls := sequenceFace(t, [][]float64{stranger, stranger, stranger, stranger}, 0)
	s := New(nil, Config{FaceServiceURL: service.URL})
	results, err := s.matchFrames(context.Background(), frames(4), scanTemplates, 2)
	if err != nil || *calls != 4 {
		t.Fatalf("all frames should be checked: %v calls=%d", err, *calls)
	}
	verdict := decideFrames(results, 4, 0.45)
	status, result, reason := scanStatus(verdict, true, false, 2)
	if status != "Ditolak" || result != ScanResultUnknown || !strings.Contains(reason, "tidak terdaftar") {
		t.Fatalf("got %s/%s %q", status, result, reason)
	}
	if scanDisplayMessage(result, reason) != "Wajah tidak Terdaftar" {
		t.Fatal("TFT message must be 'Wajah tidak Terdaftar'")
	}
}

func TestMatchFramesNoFaceAndErrors(t *testing.T) {
	service, _ := sequenceFace(t, nil, 0)
	results, err := New(nil, Config{FaceServiceURL: service.URL}).matchFrames(context.Background(), frames(3), scanTemplates, 2)
	if err != nil {
		t.Fatal(err)
	}
	verdict := decideFrames(results, 3, 0.45)
	if _, result, _ := scanStatus(verdict, true, false, 2); result != ScanResultNoFace {
		t.Fatalf("want no-face result, got %s", result)
	}
	down, _ := sequenceFace(t, nil, http.StatusServiceUnavailable)
	if _, err := New(nil, Config{FaceServiceURL: down.URL}).matchFrames(context.Background(), frames(2), scanTemplates, 2); !errors.Is(err, ErrFaceService) {
		t.Fatalf("503 must stop with ErrFaceService, got %v", err)
	}
}

func TestScanStatusRules(t *testing.T) {
	one := ScanVerdict{StudentID: "s", FramesTotal: 4, FramesWithFace: 3, FramesMatched: 1}
	two := ScanVerdict{StudentID: "s", FramesTotal: 4, FramesWithFace: 3, FramesMatched: 2}
	cases := []struct {
		name     string
		verdict  ScanVerdict
		member   bool
		liveness bool
		want     string
	}{
		{"consistent frames", two, true, false, "Hadir"},
		{"single frame", one, true, false, "Perlu ditinjau"},
		{"single frame + device liveness", one, true, true, "Hadir"},
		{"other class on fixed-class device", two, false, false, "Perlu ditinjau"},
	}
	for _, tc := range cases {
		if got, _, _ := scanStatus(tc.verdict, tc.member, tc.liveness, 2); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestDecideFramesPicksMajority(t *testing.T) {
	results := []frameMatch{
		{HasFace: true, StudentID: "a", Score: 0.9, Matched: true},
		{HasFace: true, StudentID: "b", Score: 0.6, Matched: true},
		{HasFace: true, StudentID: "b", Score: 0.7, Matched: true},
	}
	verdict := decideFrames(results, 3, 0.45)
	if verdict.StudentID != "b" || verdict.FramesMatched != 2 || verdict.BestFrame != 2 || verdict.Confidence != 0.7 {
		t.Fatalf("majority should win: %+v", verdict)
	}
}

func TestFramesHashAndSignature(t *testing.T) {
	single := []byte("jpeg")
	sum := sha256.Sum256(single)
	if framesHash([][]byte{single}) != hex.EncodeToString(sum[:]) {
		t.Fatal("single frame hash must equal SHA-256 of the image (old contract)")
	}
	joined := sha256.Sum256([]byte("ab"))
	if framesHash([][]byte{[]byte("a"), []byte("b")}) != hex.EncodeToString(joined[:]) {
		t.Fatal("multi-frame hash is SHA-256 of concatenated frames")
	}

	s := New(nil, Config{DeviceToken: "token-rahasia"})
	parts := [][]byte{[]byte("f1"), []byte("f2")}
	signed := func(ts string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("X-Device-Timestamp", ts)
		// class_id kosong = kelas otomatis; baris ketiga pesan tetap ada (kosong).
		message := strings.Join([]string{ts, "ESP32-GED-A1", "", ts, "false", framesHash(parts)}, "\n")
		mac := hmac.New(sha256.New, []byte("token-rahasia"))
		mac.Write([]byte(message))
		r.Header.Set("X-Device-Signature", hex.EncodeToString(mac.Sum(nil)))
		return r
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if !s.validScanSignature(signed(now), "ESP32-GED-A1", "", now, "false", parts) {
		t.Fatal("valid multi-frame signature rejected")
	}
	if s.validScanSignature(signed(now), "ESP32-GED-A1", "", now, "false", parts[:1]) {
		t.Fatal("dropping a frame must invalidate the signature")
	}
	old := "2026-01-01T00:00:00Z"
	if s.validScanSignature(signed(old), "ESP32-GED-A1", "", old, "false", parts) {
		t.Fatal("stale timestamp must be rejected")
	}
}

func TestReadScanFrames(t *testing.T) {
	build := func(n int) *http.Request {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		_ = w.WriteField("device_id", "ESP32-GED-A1")
		for i := 0; i < n; i++ {
			part, _ := w.CreateFormFile("image", "frame.jpg")
			_, _ = part.Write([]byte{byte(i)})
		}
		_ = w.Close()
		r := httptest.NewRequest(http.MethodPost, "/", &body)
		r.Header.Set("Content-Type", w.FormDataContentType())
		return r
	}
	r := build(4)
	got, err := readScanFrames(httptest.NewRecorder(), r)
	if err != nil || len(got) != 4 || got[3][0] != 3 || r.FormValue("device_id") != "ESP32-GED-A1" {
		t.Fatalf("4 frames in order expected: %v %d", err, len(got))
	}
	if _, err := readScanFrames(httptest.NewRecorder(), build(MaxScanFrames+1)); err == nil {
		t.Fatal("too many frames must be rejected")
	}
	if _, err := readScanFrames(httptest.NewRecorder(), build(0)); err == nil {
		t.Fatal("zero frames must be rejected")
	}
}
