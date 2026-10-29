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
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Integrasi dengan face service Python (services/face/api.py).
//
// Face service bersifat stateless: ia mendeteksi wajah (YOLO-FaceV2), memilih
// wajah dengan confidence tertinggi, lalu mengekstrak embedding ArcFace 512-d
// yang sudah dinormalisasi L2. Go memakai dua endpoint:
//
//   - POST /embed    — satu frame scan → satu embedding (lihat scan.go).
//   - POST /register — 1–6 foto enrollment (pose berbeda) → satu embedding per foto.
//
// Penyimpanan template dan pencocokan 1:N dikerjakan di Go, sehingga Go menjadi
// satu-satunya API publik untuk dashboard maupun perangkat ESP32, dan face
// service cukup berjalan di jaringan internal.

// MaxEnrollPhotos adalah batas foto per enrollment (batas /register di Python).
const MaxEnrollPhotos = 6

var (
	ErrNoFace              = errors.New("no face detected")
	ErrInvalidImage        = errors.New("image cannot be decoded")
	ErrFaceAlreadyEnrolled = errors.New("face already enrolled for another student")
	ErrInconsistentFaces   = errors.New("enrollment photos show different faces")
	ErrTooManyPhotos       = errors.New("too many enrollment photos")
)

// PhotoError menandai foto enrollment ke-N (mulai 1) yang menyebabkan error.
// errors.Is tetap bekerja terhadap error dasarnya (ErrNoFace, dst.).
type PhotoError struct {
	Photo int
	Err   error
}

func (e *PhotoError) Error() string { return fmt.Sprintf("photo %d: %v", e.Photo, e.Err) }
func (e *PhotoError) Unwrap() error { return e.Err }

// PhotoNumber mengembalikan nomor foto (mulai 1) dari err, atau 0 bila tidak ada.
func PhotoNumber(err error) int {
	var photoErr *PhotoError
	if errors.As(err, &photoErr) {
		return photoErr.Photo
	}
	return 0
}

// EnrollImage adalah satu foto enrollment yang diunggah administrator.
type EnrollImage struct {
	Data     []byte
	Filename string
}

// faceServiceError membawa status HTTP dan pesan "detail" dari face service
// agar pemanggil bisa membedakan gambar rusak (400), tanpa wajah (422), dan
// layanan yang mati/bermasalah.
type faceServiceError struct {
	Status int
	Detail string
}

func (e *faceServiceError) Error() string {
	return fmt.Sprintf("face service returned %d: %s", e.Status, e.Detail)
}

// embedResponse mengikuti skema EmbedResponse di services/face/api.py.
type embedResponse struct {
	Embedding        []float64 `json:"embedding"`
	Confidence       float64   `json:"confidence"`
	ProcessingTimeMS float64   `json:"processing_time_ms"`
}

// registerResponse mengikuti skema RegisterResponse di services/face/api.py.
// aligned_face_b64 tidak diminta (return_aligned=false) sehingga tidak dibaca.
type registerResponse struct {
	StudentID    string `json:"student_id"`
	NumTemplates int    `json:"num_templates"`
	Templates    []struct {
		Embedding  []float64 `json:"embedding"`
		Confidence float64   `json:"confidence"`
	} `json:"templates"`
	ProcessingTimeMS float64 `json:"processing_time_ms"`
}

// faceTemplate adalah satu dokumen face_templates. Enrollment baru mengisi
// Encodings (1–6 vektor, satu per pose); dokumen lama hanya punya Encoding.
type faceTemplate struct {
	StudentID string      `bson:"student_id"`
	Encoding  []float64   `bson:"encoding,omitempty"`
	Encodings [][]float64 `bson:"encodings,omitempty"`
}

// vectors mengembalikan semua embedding milik template.
func (t faceTemplate) vectors() [][]float64 {
	if len(t.Encodings) > 0 {
		return t.Encodings
	}
	if len(t.Encoding) > 0 {
		return [][]float64{t.Encoding}
	}
	return nil
}

// postImages mengirim satu atau beberapa file sebagai multipart ke face
// service lalu men-decode respons JSON ke out. Batas waktunya
// FaceServiceTimeout per foto, jadi /register 6 foto mendapat 6× waktu /embed.
func (s *Server) postImages(ctx context.Context, path string, query url.Values, field string, images []EnrollImage, out any) error {
	ctx, cancel := context.WithTimeout(ctx, s.config.FaceServiceTimeout*time.Duration(max(len(images), 1)))
	defer cancel()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, image := range images {
		part, err := writer.CreateFormFile(field, image.Filename)
		if err != nil {
			return err
		}
		if _, err = part.Write(image.Data); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	endpoint := strings.TrimRight(s.config.FaceServiceURL, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if s.config.FaceServiceToken != "" {
		request.Header.Set("Authorization", "Bearer "+s.config.FaceServiceToken)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return &faceServiceError{Status: response.StatusCode, Detail: errorDetail(raw)}
	}
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("decode face service response: %w", err)
	}
	return nil
}

// errorDetail mengambil field "detail" (string) dari error FastAPI; bila
// bukan string (mis. error validasi berupa list) seluruh body dikembalikan.
func errorDetail(raw []byte) string {
	var payload struct {
		Detail any `json:"detail"`
	}
	if json.Unmarshal(raw, &payload) == nil {
		if text, ok := payload.Detail.(string); ok {
			return text
		}
	}
	return strings.TrimSpace(string(raw))
}

// photoIndexPattern membaca nomor foto dari pesan /register, mis.
// "No face detected in image #2 ('foto-2.jpg')".
var photoIndexPattern = regexp.MustCompile(`image #(\d+)`)

// classifyFaceError memetakan error face service ke error domain:
// 400 → ErrInvalidImage, 422 "No face detected" → ErrNoFace (dengan nomor
// foto bila ada), selain itu → ErrFaceService.
func classifyFaceError(err error) error {
	var serviceErr *faceServiceError
	if !errors.As(err, &serviceErr) {
		return fmt.Errorf("%w: %v", ErrFaceService, err)
	}
	var base error
	switch {
	case serviceErr.Status == http.StatusBadRequest:
		base = ErrInvalidImage
	case serviceErr.Status == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(serviceErr.Detail), "no face"):
		base = ErrNoFace
	default:
		return fmt.Errorf("%w: %v", ErrFaceService, err)
	}
	if match := photoIndexPattern.FindStringSubmatch(serviceErr.Detail); match != nil {
		if n, convErr := strconv.Atoi(match[1]); convErr == nil {
			return &PhotoError{Photo: n, Err: base}
		}
	}
	return base
}

// embedFace memanggil POST /embed (field "image") untuk satu capture.
func (s *Server) embedFace(ctx context.Context, image []byte, filename string) (embedResponse, error) {
	if filename == "" {
		filename = "capture.jpg"
	}
	var result embedResponse
	err := s.postImages(ctx, "/embed", nil, "image", []EnrollImage{{Data: image, Filename: filename}}, &result)
	return result, err
}

// registerFaces memanggil POST /register?student_id=... (field "images",
// 1–6 file). Nama file diseragamkan menjadi foto-N.<ext> agar nomor foto pada
// pesan error sesuai urutan unggahan.
func (s *Server) registerFaces(ctx context.Context, studentID string, images []EnrollImage) (registerResponse, error) {
	files := make([]EnrollImage, len(images))
	for i, image := range images {
		ext, _ := imageExtension(image.Data)
		files[i] = EnrollImage{Data: image.Data, Filename: fmt.Sprintf("foto-%d.%s", i+1, ext)}
	}
	var result registerResponse
	err := s.postImages(ctx, "/register", url.Values{"student_id": {studentID}}, "images", files, &result)
	return result, err
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

// bestTemplateMatch mencari mahasiswa dengan kemiripan tertinggi. Skor seorang
// mahasiswa adalah skor tertinggi di antara semua embedding pose-nya.
// excludeStudent dipakai saat enrollment agar template milik mahasiswa itu
// sendiri diabaikan.
func bestTemplateMatch(embedding []float64, templates []faceTemplate, excludeStudent string) (string, float64, bool) {
	bestID, bestScore, found := "", -1.0, false
	for _, template := range templates {
		if template.StudentID == "" || template.StudentID == excludeStudent {
			continue
		}
		for _, vector := range template.vectors() {
			score, ok := cosineSimilarity(embedding, vector)
			if !ok {
				continue
			}
			if !found || score > bestScore {
				bestID, bestScore, found = template.StudentID, score, true
			}
		}
	}
	return bestID, bestScore, found
}

func (s *Server) loadTemplates(ctx context.Context) ([]faceTemplate, error) {
	cursor, err := s.db.Collection("face_templates").Find(ctx, bson.M{},
		options.Find().SetProjection(bson.M{"student_id": 1, "encoding": 1, "encodings": 1}))
	if err != nil {
		return nil, err
	}
	var templates []faceTemplate
	if err := cursor.All(ctx, &templates); err != nil {
		return nil, err
	}
	return templates, nil
}

// poseConsistency mengembalikan kemiripan foto ke-i dengan foto lain dalam
// satu enrollment: nilai tertinggi antara kemiripan dengan foto pertama
// (depan) dan dengan rata-rata semua foto lain. Rata-rata membantu pose
// miring yang kurang mirip foto depan tetapi mirip pose-pose lainnya; foto
// orang lain tetap rendah terhadap keduanya.
func poseConsistency(embeddings [][]float64, i int) float64 {
	best, _ := cosineSimilarity(embeddings[0], embeddings[i])
	mean := make([]float64, len(embeddings[i]))
	others := 0
	for j, other := range embeddings {
		if j == i || len(other) != len(mean) {
			continue
		}
		for k, v := range other {
			mean[k] += v
		}
		others++
	}
	if others > 1 {
		if score, ok := cosineSimilarity(mean, embeddings[i]); ok && score > best {
			best = score
		}
	}
	return best
}

// checkEnrollEmbeddings memvalidasi hasil /register sebelum disimpan:
// setiap foto harus orang yang sama dengan foto lainnya (poseConsistency ≥
// poseThreshold) dan tidak boleh cocok dengan template mahasiswa lain
// (cosine ≥ simThreshold).
func checkEnrollEmbeddings(embeddings [][]float64, templates []faceTemplate, studentID string, simThreshold, poseThreshold float64) (map[string]any, error) {
	for i := 1; i < len(embeddings); i++ {
		if score := poseConsistency(embeddings, i); score < poseThreshold {
			return map[string]any{"photo": i + 1, "similarity": round4(score)}, &PhotoError{Photo: i + 1, Err: ErrInconsistentFaces}
		}
	}
	for i, embedding := range embeddings {
		if otherID, score, ok := bestTemplateMatch(embedding, templates, studentID); ok && score >= simThreshold {
			return map[string]any{"conflictStudentId": otherID, "similarity": round4(score), "photo": i + 1},
				&PhotoError{Photo: i + 1, Err: ErrFaceAlreadyEnrolled}
		}
	}
	return nil, nil
}

// enrollEmbeddings mengirim 1–6 foto ke /register, memvalidasi hasilnya, lalu
// menyimpan semua embedding sebagai satu dokumen face_templates (field
// encodings). Enrollment ulang mengganti seluruh embedding lama.
func (s *Server) enrollEmbeddings(ctx context.Context, studentID string, images []EnrollImage) (map[string]any, error) {
	switch {
	case len(images) == 0:
		return nil, ErrInvalidImage
	case len(images) > MaxEnrollPhotos:
		return nil, ErrTooManyPhotos
	}
	registration, err := s.registerFaces(ctx, studentID, images)
	if err != nil {
		return nil, classifyFaceError(err)
	}
	if len(registration.Templates) != len(images) {
		return nil, fmt.Errorf("%w: face service mengembalikan %d template untuk %d foto", ErrFaceService, len(registration.Templates), len(images))
	}
	embeddings := make([][]float64, 0, len(images))
	confidences := make([]float64, 0, len(images))
	for i, template := range registration.Templates {
		if len(template.Embedding) == 0 {
			return nil, &PhotoError{Photo: i + 1, Err: ErrNoFace}
		}
		embeddings = append(embeddings, template.Embedding)
		confidences = append(confidences, round4(template.Confidence))
	}

	templates, err := s.loadTemplates(ctx)
	if err != nil {
		return nil, err
	}
	if detail, err := checkEnrollEmbeddings(embeddings, templates, studentID, s.config.SimThreshold, s.config.PoseThreshold); err != nil {
		return detail, err
	}

	now := time.Now().UTC()
	_, err = s.db.Collection("face_templates").UpdateOne(ctx,
		bson.M{"student_id": studentID},
		bson.M{
			"$set": bson.M{
				"encodings":             embeddings,
				"template_count":        len(embeddings),
				"dimension":             len(embeddings[0]),
				"detection_confidences": confidences,
				"updated_at":            now,
			},
			"$unset":       bson.M{"encoding": "", "detection_confidence": ""},
			"$setOnInsert": bson.M{"student_id": studentID, "created_at": now},
		},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"studentId":            studentID,
		"templateCount":        len(embeddings),
		"dimension":            len(embeddings[0]),
		"detectionConfidences": confidences,
		"processingTimeMs":     registration.ProcessingTimeMS,
	}, nil
}

func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}
