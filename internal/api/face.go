package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Integrasi dengan face service Python (services/face/api.py).
//
// Face service bersifat stateless: ia hanya mendeteksi wajah (YOLO-FaceV2) dan
// mengekstrak embedding ArcFace 512-d yang sudah dinormalisasi L2. Penyimpanan
// template dan pencocokan 1:N dikerjakan di Go, sehingga Go menjadi satu-satunya
// API publik untuk dashboard maupun perangkat ESP32, dan face service cukup
// berjalan di jaringan internal.

var (
	ErrNoFace              = errors.New("no face detected")
	ErrMultipleFaces       = errors.New("multiple faces detected")
	ErrInvalidImage        = errors.New("image cannot be decoded")
	ErrFaceAlreadyEnrolled = errors.New("face already enrolled for another student")
)

// faceServiceError membawa status HTTP dari face service agar pemanggil bisa
// membedakan gambar rusak (400) dari layanan yang mati/bermasalah.
type faceServiceError struct {
	Status int
	Detail string
}

func (e *faceServiceError) Error() string {
	return fmt.Sprintf("face service returned %d: %s", e.Status, e.Detail)
}

// detectedFace mengikuti skema DetectedFace di services/face/api.py.
type detectedFace struct {
	Confidence float64   `json:"confidence"`
	Embedding  []float64 `json:"embedding"`
}

type detectResponse struct {
	NumFaces         int            `json:"num_faces"`
	Faces            []detectedFace `json:"faces"`
	ProcessingTimeMS float64        `json:"processing_time_ms"`
}

// FaceMatch adalah hasil pencocokan satu capture terhadap template tersimpan.
type FaceMatch struct {
	Verified   bool
	StudentID  string
	Confidence float64 // cosine similarity terbaik (0 bila tidak ada kandidat)
	Reason     string
}

type faceTemplate struct {
	StudentID string    `bson:"student_id"`
	Encoding  []float64 `bson:"encoding"`
}

// detectFaces memanggil POST /detect pada face service.
func (s *Server) detectFaces(ctx context.Context, image []byte, filename string) (detectResponse, error) {
	var result detectResponse
	if filename == "" {
		filename = "capture.jpg"
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", filename)
	if err != nil {
		return result, err
	}
	if _, err = part.Write(image); err != nil {
		return result, err
	}
	if err = writer.Close(); err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.config.FaceServiceURL, "/")+"/detect", &body)
	if err != nil {
		return result, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if s.config.FaceServiceToken != "" {
		request.Header.Set("Authorization", "Bearer "+s.config.FaceServiceToken)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return result, &faceServiceError{Status: response.StatusCode, Detail: strings.TrimSpace(string(detail))}
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("decode face service response: %w", err)
	}
	return result, nil
}

// primaryFace memilih wajah dengan confidence deteksi tertinggi.
func primaryFace(faces []detectedFace) (detectedFace, bool) {
	best, found := detectedFace{}, false
	for _, face := range faces {
		if len(face.Embedding) == 0 {
			continue
		}
		if !found || face.Confidence > best.Confidence {
			best, found = face, true
		}
	}
	return best, found
}

// cosineSimilarity menghitung kemiripan dua vektor. Embedding ArcFace sudah
// dinormalisasi L2, tetapi pembagian norma tetap dilakukan agar aman untuk
// template dari model lain. Mengembalikan false bila dimensi berbeda.
func cosineSimilarity(a, b []float64) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0, false
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), true
}

// bestTemplateMatch mencari template dengan kemiripan tertinggi. excludeStudent
// dipakai saat enrollment agar template milik mahasiswa itu sendiri diabaikan.
func bestTemplateMatch(embedding []float64, templates []faceTemplate, excludeStudent string) (string, float64, bool) {
	bestID, bestScore, found := "", -1.0, false
	for _, template := range templates {
		if template.StudentID == "" || template.StudentID == excludeStudent {
			continue
		}
		score, ok := cosineSimilarity(embedding, template.Encoding)
		if !ok {
			continue
		}
		if !found || score > bestScore {
			bestID, bestScore, found = template.StudentID, score, true
		}
	}
	return bestID, bestScore, found
}

func (s *Server) loadTemplates(ctx context.Context) ([]faceTemplate, error) {
	cursor, err := s.db.Collection("face_templates").Find(ctx, bson.M{},
		options.Find().SetProjection(bson.M{"student_id": 1, "encoding": 1}))
	if err != nil {
		return nil, err
	}
	var templates []faceTemplate
	if err := cursor.All(ctx, &templates); err != nil {
		return nil, err
	}
	return templates, nil
}

// enrollEmbedding mendeteksi wajah pada foto enrollment (wajib tepat satu
// wajah), memastikan wajah itu belum terdaftar atas nama mahasiswa lain, lalu
// menyimpan embedding ke koleksi face_templates.
func (s *Server) enrollEmbedding(ctx context.Context, studentID string, image []byte, filename string) (map[string]any, error) {
	detection, err := s.detectFaces(ctx, image, filename)
	if err != nil {
		var serviceErr *faceServiceError
		if errors.As(err, &serviceErr) && serviceErr.Status == http.StatusBadRequest {
			return nil, ErrInvalidImage
		}
		return nil, fmt.Errorf("%w: %v", ErrFaceService, err)
	}
	faces := 0
	for _, face := range detection.Faces {
		if len(face.Embedding) > 0 {
			faces++
		}
	}
	switch {
	case faces == 0:
		return nil, ErrNoFace
	case faces > 1:
		return nil, ErrMultipleFaces
	}
	face, _ := primaryFace(detection.Faces)

	templates, err := s.loadTemplates(ctx)
	if err != nil {
		return nil, err
	}
	if otherID, score, ok := bestTemplateMatch(face.Embedding, templates, studentID); ok && score >= s.config.SimThreshold {
		return map[string]any{"conflictStudentId": otherID, "similarity": round4(score)}, ErrFaceAlreadyEnrolled
	}

	now := time.Now().UTC()
	_, err = s.db.Collection("face_templates").UpdateOne(ctx,
		bson.M{"student_id": studentID},
		bson.M{
			"$set": bson.M{
				"encoding":             face.Embedding,
				"dimension":            len(face.Embedding),
				"detection_confidence": face.Confidence,
				"updated_at":           now,
			},
			"$setOnInsert": bson.M{"student_id": studentID, "created_at": now},
		},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"studentId":           studentID,
		"dimension":           len(face.Embedding),
		"detectionConfidence": round4(face.Confidence),
		"processingTimeMs":    detection.ProcessingTimeMS,
	}, nil
}

// verifyCapture mencocokkan capture perangkat terhadap seluruh template (1:N).
// Keanggotaan kelas diperiksa terpisah oleh pemanggil.
func (s *Server) verifyCapture(ctx context.Context, image []byte, filename string) (FaceMatch, error) {
	detection, err := s.detectFaces(ctx, image, filename)
	if err != nil {
		var serviceErr *faceServiceError
		if errors.As(err, &serviceErr) && serviceErr.Status == http.StatusBadRequest {
			return FaceMatch{}, ErrInvalidImage
		}
		return FaceMatch{}, fmt.Errorf("%w: %v", ErrFaceService, err)
	}
	face, ok := primaryFace(detection.Faces)
	if !ok {
		return FaceMatch{Reason: "Wajah tidak terdeteksi pada gambar."}, nil
	}
	templates, err := s.loadTemplates(ctx)
	if err != nil {
		return FaceMatch{}, err
	}
	studentID, score, found := bestTemplateMatch(face.Embedding, templates, "")
	if !found {
		return FaceMatch{Reason: "Belum ada template wajah yang cocok untuk dibandingkan."}, nil
	}
	if score < s.config.SimThreshold {
		return FaceMatch{
			Confidence: round4(score),
			Reason:     fmt.Sprintf("Wajah tidak cocok dengan mahasiswa terdaftar (kemiripan %.2f < %.2f).", score, s.config.SimThreshold),
		}, nil
	}
	return FaceMatch{Verified: true, StudentID: studentID, Confidence: round4(score)}, nil
}

func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}
