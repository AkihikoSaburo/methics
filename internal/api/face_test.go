package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func unit(values ...float64) []float64 {
	var norm float64
	for _, v := range values {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = v / norm
	}
	return out
}

func TestCosineSimilarity(t *testing.T) {
	if got, ok := cosineSimilarity(unit(1, 0), unit(1, 0)); !ok || math.Abs(got-1) > 1e-9 {
		t.Fatalf("identical vectors: got %v %v", got, ok)
	}
	if got, ok := cosineSimilarity(unit(1, 0), unit(0, 1)); !ok || math.Abs(got) > 1e-9 {
		t.Fatalf("orthogonal vectors: got %v %v", got, ok)
	}
	if _, ok := cosineSimilarity(unit(1, 0), unit(1, 0, 0)); ok {
		t.Fatal("dimension mismatch must be rejected")
	}
	if _, ok := cosineSimilarity([]float64{0, 0}, unit(1, 0)); ok {
		t.Fatal("zero vector must be rejected")
	}
}

func TestBestTemplateMatch(t *testing.T) {
	templates := []faceTemplate{
		{StudentID: "student-a", Encoding: unit(1, 0, 0)},
		{StudentID: "student-b", Encoding: unit(0.9, 0.1, 0)},
		{StudentID: "student-c", Encoding: unit(0, 0, 1)},
		{StudentID: "student-old", Encoding: unit(1, 0)}, // dimensi lain: diabaikan
	}
	id, score, ok := bestTemplateMatch(unit(1, 0, 0), templates, "")
	if !ok || id != "student-a" || score < 0.999 {
		t.Fatalf("got %q %.3f %v", id, score, ok)
	}
	id, _, ok = bestTemplateMatch(unit(1, 0, 0), templates, "student-a")
	if !ok || id != "student-b" {
		t.Fatalf("exclude: got %q %v", id, ok)
	}
	if _, _, ok := bestTemplateMatch(unit(1, 0, 0), nil, ""); ok {
		t.Fatal("no templates must return not found")
	}
}

func TestPrimaryFace(t *testing.T) {
	face, ok := primaryFace([]detectedFace{
		{Confidence: 0.6, Embedding: unit(1, 0)},
		{Confidence: 0.9, Embedding: unit(0, 1)},
		{Confidence: 0.99}, // tanpa embedding: diabaikan
	})
	if !ok || face.Confidence != 0.9 {
		t.Fatalf("got %+v %v", face, ok)
	}
	if _, ok := primaryFace(nil); ok {
		t.Fatal("empty list must return false")
	}
}

// fakeFaceService meniru skema services/face/api.py (DetectResponse & HealthResponse).
func fakeFaceService(t *testing.T, detectStatus int, faces []map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok", "uptime_seconds": 1.5, "model_loaded": true,
			"det_model": "face_detection/yolo-facev2s.onnx", "rec_model": "face_recognition/arcface_r50_fp16.onnx",
		})
	})
	mux.HandleFunc("POST /detect", func(w http.ResponseWriter, r *http.Request) {
		file, _, err := r.FormFile("image")
		if err != nil {
			http.Error(w, `{"detail":"image field missing"}`, http.StatusUnprocessableEntity)
			return
		}
		body, _ := io.ReadAll(file)
		if detectStatus != http.StatusOK {
			w.WriteHeader(detectStatus)
			_ = json.NewEncoder(w).Encode(map[string]any{"detail": "Cannot decode image."})
			return
		}
		if len(body) == 0 {
			t.Error("image bytes not forwarded")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"num_faces": len(faces), "faces": faces, "processing_time_ms": 12.5})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func face(conf float64, emb []float64) map[string]any {
	return map[string]any{
		"bbox":       map[string]float64{"x1": 1, "y1": 2, "x2": 30, "y2": 40},
		"confidence": conf, "embedding": emb, "aligned_face_b64": nil,
	}
}

func TestDetectFacesParsesPythonSchema(t *testing.T) {
	service := fakeFaceService(t, http.StatusOK, []map[string]any{face(0.91, unit(1, 2, 3))})
	s := New(nil, Config{FaceServiceURL: service.URL})
	got, err := s.detectFaces(context.Background(), []byte("jpeg-bytes"), "x.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if got.NumFaces != 1 || len(got.Faces) != 1 || len(got.Faces[0].Embedding) != 3 || got.Faces[0].Confidence != 0.91 {
		t.Fatalf("unexpected parse: %+v", got)
	}
}

func TestDetectFacesReportsStatus(t *testing.T) {
	service := fakeFaceService(t, http.StatusBadRequest, nil)
	s := New(nil, Config{FaceServiceURL: service.URL})
	_, err := s.detectFaces(context.Background(), []byte("bad"), "x.jpg")
	var serviceErr *faceServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 faceServiceError, got %v", err)
	}
}

func TestEnrollRejectsZeroAndMultipleFaces(t *testing.T) {
	cases := map[string]struct {
		faces []map[string]any
		want  error
	}{
		"none":     {nil, ErrNoFace},
		"multiple": {[]map[string]any{face(0.9, unit(1, 0)), face(0.8, unit(0, 1))}, ErrMultipleFaces},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			service := fakeFaceService(t, http.StatusOK, tc.faces)
			s := New(nil, Config{FaceServiceURL: service.URL})
			if _, err := s.enrollEmbedding(context.Background(), "student-x", []byte("img"), "x.jpg"); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestFaceServiceErrorsMapToSentinels(t *testing.T) {
	bad := fakeFaceService(t, http.StatusBadRequest, nil)
	s := New(nil, Config{FaceServiceURL: bad.URL})
	if _, err := s.verifyCapture(context.Background(), []byte("img"), "x.jpg"); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("400 should map to ErrInvalidImage, got %v", err)
	}
	down := New(nil, Config{FaceServiceURL: "http://127.0.0.1:1"})
	if _, err := down.verifyCapture(context.Background(), []byte("img"), "x.jpg"); !errors.Is(err, ErrFaceService) {
		t.Fatalf("unreachable service should map to ErrFaceService, got %v", err)
	}
	none := fakeFaceService(t, http.StatusOK, nil)
	match, err := New(nil, Config{FaceServiceURL: none.URL}).verifyCapture(context.Background(), []byte("img"), "x.jpg")
	if err != nil || match.Verified || match.Reason == "" {
		t.Fatalf("no face should be a rejected match, got %+v %v", match, err)
	}
}

func TestFaceHealth(t *testing.T) {
	service := fakeFaceService(t, http.StatusOK, nil)
	health := New(nil, Config{FaceServiceURL: service.URL}).faceHealth(context.Background())
	if health["online"] != true || health["recognitionModel"] != "face_recognition/arcface_r50_fp16.onnx" {
		t.Fatalf("unexpected health: %+v", health)
	}
	if New(nil, Config{FaceServiceURL: "http://127.0.0.1:1"}).faceHealth(context.Background())["online"] != false {
		t.Fatal("unreachable service must be offline")
	}
}

func TestConfigDefaults(t *testing.T) {
	s := New(nil, Config{})
	if s.config.SimThreshold != 0.45 || s.client.Timeout == 0 {
		t.Fatalf("defaults not applied: %+v", s.config)
	}
}
