package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestBestTemplateMatchUsesEveryPose(t *testing.T) {
	templates := []faceTemplate{
		{StudentID: "student-pose", Encodings: [][]float64{unit(0, 1, 0), unit(0, 0, 1)}},
		{StudentID: "student-legacy", Encoding: unit(1, 1, 0)},
	}
	id, score, ok := bestTemplateMatch(unit(0, 0, 1), templates, "")
	if !ok || id != "student-pose" || score < 0.999 {
		t.Fatalf("second pose should match: got %q %.3f %v", id, score, ok)
	}
	id, _, ok = bestTemplateMatch(unit(1, 1, 0), templates, "")
	if !ok || id != "student-legacy" {
		t.Fatalf("legacy single encoding should still match: got %q %v", id, ok)
	}
}

// fakeFaceService meniru services/face/api.py: GET /health, POST /embed
// (field image) dan POST /register?student_id= (field images, 1–6 file),
// termasuk pesan error 422 "No face detected ...".
type fakeFace struct {
	status    int         // status untuk /embed dan /register (0 = 200)
	detail    any         // isi "detail" saat status bukan 200
	embedding []float64   // hasil /embed
	templates [][]float64 // hasil /register, satu per foto
	gotFiles  []string    // nama file yang diterima /register
	gotID     string      // student_id yang diterima /register
}

func (f *fakeFace) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok", "uptime_seconds": 1.5, "model_loaded": true,
			"det_model": "face_detection/yolo-facev2s.onnx", "rec_model": "face_recognition/arcface_r50_fp16.onnx",
		})
	})
	fail := func(w http.ResponseWriter) bool {
		if f.status == 0 || f.status == http.StatusOK {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_ = json.NewEncoder(w).Encode(map[string]any{"detail": f.detail})
		return true
	}
	mux.HandleFunc("POST /embed", func(w http.ResponseWriter, r *http.Request) {
		file, _, err := r.FormFile("image")
		if err != nil {
			http.Error(w, `{"detail":[{"msg":"Field required"}]}`, http.StatusUnprocessableEntity)
			return
		}
		if body, _ := io.ReadAll(file); len(body) == 0 {
			t.Error("image bytes not forwarded")
		}
		if fail(w) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": f.embedding, "confidence": 0.91, "processing_time_ms": 12.5})
	})
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil || len(r.MultipartForm.File["images"]) == 0 {
			http.Error(w, `{"detail":[{"msg":"Field required"}]}`, http.StatusUnprocessableEntity)
			return
		}
		f.gotID = r.URL.Query().Get("student_id")
		f.gotFiles = nil
		for _, header := range r.MultipartForm.File["images"] {
			f.gotFiles = append(f.gotFiles, header.Filename)
		}
		if fail(w) {
			return
		}
		templates := []map[string]any{}
		for _, emb := range f.templates {
			templates = append(templates, map[string]any{"embedding": emb, "confidence": 0.9, "aligned_face_b64": nil})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"student_id": f.gotID, "num_templates": len(templates), "templates": templates, "processing_time_ms": 40.0,
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestEmbedFaceParsesPythonSchema(t *testing.T) {
	fake := &fakeFace{embedding: unit(1, 2, 3)}
	s := New(nil, Config{FaceServiceURL: fake.server(t).URL})
	got, err := s.embedFace(context.Background(), []byte("jpeg-bytes"), "x.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Embedding) != 3 || got.Confidence != 0.91 || got.ProcessingTimeMS != 12.5 {
		t.Fatalf("unexpected parse: %+v", got)
	}
}

func TestRegisterFacesSendsEveryPhoto(t *testing.T) {
	fake := &fakeFace{templates: [][]float64{unit(1, 0), unit(1, 0.1), unit(1, 0.2)}}
	s := New(nil, Config{FaceServiceURL: fake.server(t).URL})
	images := []EnrollImage{{Data: []byte("a"), Filename: "depan.jpg"}, {Data: []byte("b")}, {Data: []byte("c")}}
	got, err := s.registerFaces(context.Background(), "student-241011001", images)
	if err != nil {
		t.Fatal(err)
	}
	if fake.gotID != "student-241011001" || len(fake.gotFiles) != 3 || fake.gotFiles[1] != "foto-2.jpg" {
		t.Fatalf("request not forwarded correctly: id=%q files=%v", fake.gotID, fake.gotFiles)
	}
	if got.NumTemplates != 3 || len(got.Templates) != 3 || len(got.Templates[2].Embedding) != 2 {
		t.Fatalf("unexpected parse: %+v", got)
	}
}

func TestClassifyFaceError(t *testing.T) {
	cases := map[string]struct {
		err   error
		want  error
		photo int
	}{
		"bad image":        {&faceServiceError{Status: 400, Detail: "Cannot decode image."}, ErrInvalidImage, 0},
		"no face (embed)":  {&faceServiceError{Status: 422, Detail: "No face detected in the uploaded image"}, ErrNoFace, 0},
		"no face photo #3": {&faceServiceError{Status: 422, Detail: "No face detected in image #3 ('foto-3.jpg'). Ensure..."}, ErrNoFace, 3},
		"validation 422":   {&faceServiceError{Status: 422, Detail: `[{"msg":"Field required"}]`}, ErrFaceService, 0},
		"not ready":        {&faceServiceError{Status: 503, Detail: "Face Recognition models are not initialized."}, ErrFaceService, 0},
		"network":          {errors.New("connection refused"), ErrFaceService, 0},
	}
	for name, tc := range cases {
		got := classifyFaceError(tc.err)
		if !errors.Is(got, tc.want) || PhotoNumber(got) != tc.photo {
			t.Errorf("%s: got %v (photo %d), want %v (photo %d)", name, got, PhotoNumber(got), tc.want, tc.photo)
		}
	}
}

func TestCheckEnrollEmbeddings(t *testing.T) {
	others := []faceTemplate{{StudentID: "student-other", Encodings: [][]float64{unit(0, 1, 0)}}}
	if _, err := checkEnrollEmbeddings([][]float64{unit(1, 0, 0), unit(1, 0.2, 0)}, others, "student-me", 0.45, 0.30); err != nil {
		t.Fatalf("consistent, unique poses must pass: %v", err)
	}
	_, err := checkEnrollEmbeddings([][]float64{unit(1, 0, 0), unit(1, 0.1, 0), unit(0, 0, 1)}, nil, "student-me", 0.45, 0.30)
	if !errors.Is(err, ErrInconsistentFaces) || PhotoNumber(err) != 3 {
		t.Fatalf("photo 3 is another person: got %v", err)
	}
	// Pose miring: kemiripan dengan foto depan ~0.38 (< 0.45) tetap diterima
	// karena di atas ambang pose 0.30.
	front := unit(1, 0, 0)
	side := unit(0.38, 0.925, 0)
	if _, err := checkEnrollEmbeddings([][]float64{front, side}, nil, "student-me", 0.45, 0.30); err != nil {
		t.Fatalf("tilted pose of the same person must pass: %v", err)
	}
	// Enam pose: pose ekstrem yang rendah terhadap foto depan tetapi mirip
	// rata-rata pose lain tetap diterima.
	poses := [][]float64{unit(1, 0.2, 0), unit(1, 0.5, 0), unit(1, 0.8, 0), unit(0.8, 1, 0), unit(0.6, 1, 0), unit(0.05, 1, 0)}
	if score, _ := cosineSimilarity(poses[0], poses[5]); score >= 0.30 {
		t.Fatalf("test setup: photo 6 should be far from photo 1, got %.2f", score)
	}
	if _, err = checkEnrollEmbeddings(poses, nil, "student-me", 0.45, 0.30); err != nil {
		t.Fatalf("six poses of one person must pass: %v", err)
	}
	_, err = checkEnrollEmbeddings([][]float64{unit(1, 0, 0), unit(1, 0.1, 0), unit(0, 0, 1)}, nil, "student-me", 0.45, 0.30)
	if !errors.Is(err, ErrInconsistentFaces) || PhotoNumber(err) != 3 {
		t.Fatalf("photo 3 is another person: got %v", err)
	}
	detail, err := checkEnrollEmbeddings([][]float64{unit(0, 1, 0)}, others, "student-me", 0.45, 0.30)
	if !errors.Is(err, ErrFaceAlreadyEnrolled) || detail["conflictStudentId"] != "student-other" {
		t.Fatalf("face of another student must be rejected: %v %v", detail, err)
	}
	// Template milik mahasiswa itu sendiri (enrollment ulang) tidak dianggap konflik.
	self := []faceTemplate{{StudentID: "student-me", Encodings: [][]float64{unit(1, 0, 0)}}}
	if _, err := checkEnrollEmbeddings([][]float64{unit(1, 0, 0)}, self, "student-me", 0.45, 0.30); err != nil {
		t.Fatalf("re-enrollment must not conflict with itself: %v", err)
	}
}

func TestEnrollEmbeddingsRejectsBadInput(t *testing.T) {
	fake := &fakeFace{status: http.StatusUnprocessableEntity, detail: "No face detected in image #2 ('foto-2.jpg'). Ensure the photo contains a clearly visible face."}
	s := New(nil, Config{FaceServiceURL: fake.server(t).URL})
	two := []EnrollImage{{Data: []byte("a")}, {Data: []byte("b")}}
	if _, err := s.enrollEmbeddings(context.Background(), "student-x", two); !errors.Is(err, ErrNoFace) || PhotoNumber(err) != 2 {
		t.Fatalf("want ErrNoFace on photo 2, got %v", err)
	}
	if !strings.Contains(EnrollErrorMessage(&PhotoError{Photo: 2, Err: ErrNoFace}, nil), "foto ke-2") {
		t.Fatal("message must name the photo")
	}
	seven := make([]EnrollImage, MaxEnrollPhotos+1)
	if _, err := s.enrollEmbeddings(context.Background(), "student-x", seven); !errors.Is(err, ErrTooManyPhotos) {
		t.Fatalf("want ErrTooManyPhotos, got %v", err)
	}
	if _, err := s.enrollEmbeddings(context.Background(), "student-x", nil); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("want ErrInvalidImage for zero photos, got %v", err)
	}
}

func TestFaceHealth(t *testing.T) {
	service := (&fakeFace{}).server(t)
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
	if s.config.SimThreshold != 0.45 || s.config.PoseThreshold != 0.30 || s.config.FaceServiceTimeout != 15*time.Second {
		t.Fatalf("defaults not applied: %+v", s.config)
	}
}

func TestRegisterTimeoutScalesWithPhotoCount(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(32 << 20)
		n := len(r.MultipartForm.File["images"])
		time.Sleep(time.Duration(n) * 70 * time.Millisecond) // ±70 ms per foto
		templates := make([]map[string]any, n)
		for i := range templates {
			templates[i] = map[string]any{"embedding": unit(1, float64(i)/10), "confidence": 0.9}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"student_id": "x", "num_templates": n, "templates": templates})
	})
	service := httptest.NewServer(mux)
	t.Cleanup(service.Close)
	// 100 ms per panggilan /embed; 6 foto (±420 ms) harus tetap lolos karena
	// /register mendapat 6 × 100 ms.
	s := New(nil, Config{FaceServiceURL: service.URL, FaceServiceTimeout: 100 * time.Millisecond})
	images := make([]EnrollImage, MaxEnrollPhotos)
	for i := range images {
		images[i] = EnrollImage{Data: []byte("img")}
	}
	got, err := s.registerFaces(context.Background(), "x", images)
	if err != nil || len(got.Templates) != MaxEnrollPhotos {
		t.Fatalf("6 photos must fit the scaled timeout: %v (%d templates)", err, len(got.Templates))
	}
}
