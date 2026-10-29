package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// Scan kehadiran dari perangkat (POST /api/v1/scans).
//
// Perangkat menyalakan kamera saat sensor ultrasonik mendeteksi orang, lalu
// mengambil beberapa frame selama ±2 detik dan mengirim semuanya dalam satu
// request (field "image" berulang). Go mencocokkan setiap frame lewat face
// service (/embed) dan memutuskan satu hasil:
//
//   - Wajah cocok dengan mahasiswa yang sama di ≥ ScanMinMatchFrames frame
//     (default 2) → Hadir di kelas mahasiswa itu.
//   - Wajah cocok tetapi hanya di 1 frame → Perlu ditinjau.
//   - Ada wajah tetapi tidak cocok dengan siapa pun → Ditolak, "Wajah tidak
//     Terdaftar".
//   - Tidak ada wajah di semua frame → Ditolak, "Wajah tidak terdeteksi".
//
// Kecocokan beberapa frame hanya membuktikan wajah yang sama terlihat
// konsisten selama ±2 detik; ini BUKAN anti-spoof (foto cetak di depan kamera
// tetap bisa lolos).

// MaxScanFrames adalah batas frame per request scan.
const MaxScanFrames = 6

// Kode hasil scan untuk perangkat (field "result" pada respons).
const (
	ScanResultPresent    = "hadir"
	ScanResultReview     = "perlu_ditinjau"
	ScanResultUnknown    = "tidak_terdaftar"
	ScanResultNoFace     = "wajah_tidak_terdeteksi"
	scanMessageUnknown   = "Wajah tidak Terdaftar"
	scanMessageNoFace    = "Wajah tidak terdeteksi"
	scanMessagePresent   = "Hadir"
	scanMessageReview    = "Perlu ditinjau"
	maxScanRequestBytes  = 16 << 20
	maxScanFrameBytes    = 8 << 20
	scanSignatureMaxSkew = 2 * time.Minute
	scanCaptureMaxSkew   = 5 * time.Minute
)

// frameMatch adalah hasil pencocokan satu frame.
type frameMatch struct {
	HasFace   bool
	StudentID string  // kandidat terbaik (kosong bila tidak ada template)
	Score     float64 // cosine similarity kandidat terbaik
	Matched   bool    // Score ≥ ambang
}

// ScanVerdict adalah keputusan atas semua frame satu scan.
type ScanVerdict struct {
	StudentID      string  // mahasiswa yang cocok (kosong bila tidak ada)
	Confidence     float64 // skor terbaik mahasiswa itu, atau skor terbaik keseluruhan
	FramesTotal    int
	FramesWithFace int
	FramesMatched  int // frame yang cocok dengan StudentID
	BestFrame      int // indeks frame yang disimpan sebagai foto bukti
	Reason         string
}

// readScanFrames membaca 1–MaxScanFrames file dari field multipart "image"
// (urutan dipertahankan). Field form lain tersedia lewat r.FormValue.
func readScanFrames(w http.ResponseWriter, r *http.Request) ([][]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxScanRequestBytes)
	if err := r.ParseMultipartForm(maxScanRequestBytes); err != nil {
		return nil, errors.New("kirim multipart/form-data dengan 1–6 file image (total maksimal 16 MB)")
	}
	headers := r.MultipartForm.File["image"]
	switch {
	case len(headers) == 0:
		return nil, errors.New("field image wajib diisi")
	case len(headers) > MaxScanFrames:
		return nil, fmt.Errorf("maksimal %d frame per scan", MaxScanFrames)
	}
	frames := make([][]byte, 0, len(headers))
	for i, header := range headers {
		file, err := header.Open()
		if err != nil {
			return nil, fmt.Errorf("frame ke-%d tidak dapat dibaca", i+1)
		}
		data, err := io.ReadAll(io.LimitReader(file, maxScanFrameBytes+1))
		file.Close()
		if err != nil || len(data) == 0 || len(data) > maxScanFrameBytes {
			return nil, fmt.Errorf("frame ke-%d kosong atau melebihi 8 MB", i+1)
		}
		frames = append(frames, data)
	}
	return frames, nil
}

// framesHash adalah SHA-256 (hex) atas semua frame yang digabung berurutan.
// Untuk satu frame hasilnya sama dengan SHA-256 gambar itu (kontrak lama).
func framesHash(frames [][]byte) string {
	h := sha256.New()
	for _, frame := range frames {
		h.Write(frame)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// validScanSignature memeriksa X-Device-Signature = HMAC-SHA256(DEVICE_TOKEN,
// timestamp \n device_id \n class_id \n captured_at \n liveness \n framesHash)
// dan X-Device-Timestamp ±2 menit.
func (s *Server) validScanSignature(r *http.Request, deviceID, classID, capturedAt, liveness string, frames [][]byte) bool {
	timestamp := strings.TrimSpace(r.Header.Get("X-Device-Timestamp"))
	requestTime, err := time.Parse(time.RFC3339, timestamp)
	if err != nil || time.Since(requestTime) > scanSignatureMaxSkew || time.Until(requestTime) > scanSignatureMaxSkew {
		return false
	}
	message := strings.Join([]string{timestamp, deviceID, classID, capturedAt, liveness, framesHash(frames)}, "\n")
	mac := hmac.New(sha256.New, []byte(s.config.DeviceToken))
	_, _ = mac.Write([]byte(message))
	expected := hex.EncodeToString(mac.Sum(nil))
	provided := strings.TrimSpace(r.Header.Get("X-Device-Signature"))
	return len(provided) == len(expected) && hmac.Equal([]byte(provided), []byte(expected))
}

// matchFrames mengirim frame satu per satu ke /embed dan mencocokkannya
// dengan templates. Berhenti lebih awal begitu satu mahasiswa cocok di
// minMatch frame. Frame tanpa wajah atau rusak dilewati; error face service
// (mati/timeout) menghentikan proses. Bila semua frame rusak → ErrInvalidImage.
func (s *Server) matchFrames(ctx context.Context, frames [][]byte, templates []faceTemplate, minMatch int) ([]frameMatch, error) {
	results := make([]frameMatch, 0, len(frames))
	votes := map[string]int{}
	invalid := 0
	for i, frame := range frames {
		embedded, err := s.embedFace(ctx, frame, fmt.Sprintf("frame-%d.jpg", i+1))
		if err != nil {
			switch err = classifyFaceError(err); {
			case errors.Is(err, ErrNoFace):
				results = append(results, frameMatch{})
				continue
			case errors.Is(err, ErrInvalidImage):
				invalid++
				results = append(results, frameMatch{})
				continue
			default:
				return nil, err
			}
		}
		if len(embedded.Embedding) == 0 {
			results = append(results, frameMatch{})
			continue
		}
		studentID, score, found := bestTemplateMatch(embedded.Embedding, templates, "")
		match := frameMatch{HasFace: true, StudentID: studentID, Score: score}
		if found && score >= s.config.SimThreshold {
			match.Matched = true
			votes[studentID]++
		}
		results = append(results, match)
		if match.Matched && votes[studentID] >= minMatch {
			break // cukup bukti; sisa frame tidak perlu diproses
		}
	}
	if invalid == len(frames) {
		return nil, ErrInvalidImage
	}
	return results, nil
}

// decideFrames merangkum hasil per frame menjadi satu keputusan. Mahasiswa
// dengan frame cocok terbanyak menang (seri → skor tertinggi).
func decideFrames(results []frameMatch, total int, threshold float64) ScanVerdict {
	verdict := ScanVerdict{FramesTotal: total}
	votes := map[string]int{}
	bestScore := map[string]float64{}
	bestFrame := map[string]int{}
	overall, overallFrame, firstFace := -1.0, 0, -1
	for i, result := range results {
		if !result.HasFace {
			continue
		}
		verdict.FramesWithFace++
		if firstFace < 0 {
			firstFace = i
		}
		if result.StudentID != "" && result.Score > overall {
			overall, overallFrame = result.Score, i
		}
		if !result.Matched {
			continue
		}
		votes[result.StudentID]++
		if score, ok := bestScore[result.StudentID]; !ok || result.Score > score {
			bestScore[result.StudentID], bestFrame[result.StudentID] = result.Score, i
		}
	}
	for id, count := range votes {
		if count > verdict.FramesMatched || (count == verdict.FramesMatched && bestScore[id] > bestScore[verdict.StudentID]) {
			verdict.StudentID, verdict.FramesMatched = id, count
		}
	}
	switch {
	case verdict.StudentID != "":
		verdict.Confidence = round4(bestScore[verdict.StudentID])
		verdict.BestFrame = bestFrame[verdict.StudentID]
	case verdict.FramesWithFace == 0:
		verdict.Reason = "Wajah tidak terdeteksi pada " + framesLabel(total) + "."
	case overall < 0:
		verdict.Reason = "Wajah tidak terdaftar: belum ada template wajah untuk dibandingkan."
		verdict.BestFrame = firstFace
	default:
		verdict.Confidence = round4(overall)
		verdict.BestFrame = overallFrame
		verdict.Reason = fmt.Sprintf("Wajah tidak terdaftar (kemiripan tertinggi %.2f < %.2f).", overall, threshold)
	}
	return verdict
}

func framesLabel(total int) string {
	if total == 1 {
		return "gambar"
	}
	return fmt.Sprintf("%d frame", total)
}

// scanStatus menentukan status dan alasan untuk mahasiswa yang cocok.
// memberOfDeviceClass false berarti perangkat memakai kelas tetap dan
// mahasiswa bukan anggota kelas itu.
func scanStatus(verdict ScanVerdict, memberOfDeviceClass, livenessPassed bool, minMatch int) (status, result, reason string) {
	switch {
	case verdict.StudentID == "" && verdict.FramesWithFace == 0:
		return "Ditolak", ScanResultNoFace, verdict.Reason
	case verdict.StudentID == "":
		return "Ditolak", ScanResultUnknown, verdict.Reason
	case !memberOfDeviceClass:
		return "Perlu ditinjau", ScanResultReview, "Mahasiswa terverifikasi tetapi bukan anggota kelas pada perangkat."
	case livenessPassed || verdict.FramesMatched >= minMatch:
		return "Hadir", ScanResultPresent, ""
	default:
		return "Perlu ditinjau", ScanResultReview, fmt.Sprintf(
			"Wajah cocok di %d dari %d frame; minimal %d frame agar langsung hadir.", verdict.FramesMatched, verdict.FramesTotal, minMatch)
	}
}

func scanDisplayMessage(result, reason string) string {
	switch result {
	case ScanResultPresent:
		return scanMessagePresent
	case ScanResultUnknown:
		return scanMessageUnknown
	case ScanResultNoFace:
		return scanMessageNoFace
	}
	if reason != "" {
		return reason
	}
	return scanMessageReview
}

// receiveScan: POST /api/v1/scans (perangkat). Urutan: token → frame →
// field → captured_at ±5 menit → liveness → HMAC + X-Device-Timestamp ±2
// menit → perangkat (& kelas bila class_id diisi) terdaftar → matchFrames
// (Python /embed per frame + cosine 1:N) → decideFrames → status → foto bukti
// ke Garage (opsional) → simpan scan_events. class_id kosong = kelas otomatis
// (kelas mahasiswa yang cocok). Face service gagal → 502 dan event tidak
// dicatat. Kontrak: API.md, docs/devices.md §6.
func (s *Server) receiveScan(w http.ResponseWriter, r *http.Request) {
	if !s.validDeviceToken(r) {
		writeError(w, http.StatusUnauthorized, "invalid_device", "Token perangkat tidak valid.")
		return
	}
	frames, err := readScanFrames(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_image", err.Error())
		return
	}
	deviceID := strings.TrimSpace(r.FormValue("device_id"))
	classID := strings.TrimSpace(strings.ToUpper(r.FormValue("class_id")))
	capturedAtRaw := strings.TrimSpace(r.FormValue("captured_at"))
	livenessRaw := strings.ToLower(strings.TrimSpace(r.FormValue("liveness_passed")))
	if deviceID == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "device_id wajib diisi.")
		return
	}
	capturedAt, parseErr := time.Parse(time.RFC3339, capturedAtRaw)
	if parseErr != nil || time.Since(capturedAt) > scanCaptureMaxSkew || time.Until(capturedAt) > scanCaptureMaxSkew {
		writeError(w, http.StatusBadRequest, "stale_capture", "captured_at wajib RFC3339 dan berjarak maksimal 5 menit dari waktu server.")
		return
	}
	if livenessRaw != "true" && livenessRaw != "false" {
		writeError(w, http.StatusBadRequest, "invalid_liveness", "liveness_passed wajib bernilai true atau false.")
		return
	}
	livenessPassed := livenessRaw == "true"
	if !s.validScanSignature(r, deviceID, classID, capturedAtRaw, livenessRaw, frames) {
		writeError(w, http.StatusUnauthorized, "invalid_signature", "Signature scan tidak valid atau timestamp kedaluwarsa.")
		return
	}
	ctx := r.Context()
	deviceCount, err := s.db.Collection("devices").CountDocuments(ctx, bson.M{"_id": deviceID})
	if err != nil || deviceCount == 0 {
		writeError(w, http.StatusForbidden, "unknown_device", "Perangkat belum terdaftar.")
		return
	}
	if classID != "" {
		classCount, err := s.db.Collection("classes").CountDocuments(ctx, bson.M{"_id": classID})
		if err != nil || classCount == 0 {
			writeError(w, http.StatusBadRequest, "unknown_class", "Kelas tidak ditemukan.")
			return
		}
	}

	templates, err := s.loadTemplates(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal memuat template wajah.")
		return
	}
	started := time.Now()
	results, err := s.matchFrames(ctx, frames, templates, s.config.ScanMinMatchFrames)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidImage):
			writeError(w, http.StatusBadRequest, "invalid_image", "Gambar tidak dapat dibaca; kirim JPEG/PNG yang valid.")
		default:
			writeError(w, http.StatusBadGateway, "face_service_error", "Verifikasi tidak tersedia; scan tidak dicatat sebagai hadir.")
		}
		return
	}
	verdict := decideFrames(results, len(frames), s.config.SimThreshold)

	// Kelas event: kelas tetap perangkat, atau kelas mahasiswa yang cocok.
	eventClass := classID
	member := true
	var student struct {
		NIM     string `bson:"nim"`
		Name    string `bson:"name"`
		ClassID string `bson:"class_id"`
	}
	if verdict.StudentID != "" {
		if err := s.db.Collection("students").FindOne(ctx, bson.M{"_id": verdict.StudentID}).Decode(&student); err != nil {
			writeError(w, http.StatusInternalServerError, "database_error", "Gagal memuat data mahasiswa.")
			return
		}
		if classID == "" {
			eventClass = student.ClassID
		} else {
			member = student.ClassID == classID
		}
	}
	status, result, reason := scanStatus(verdict, member, livenessPassed, s.config.ScanMinMatchFrames)
	faceVerified := verdict.StudentID != ""

	scannedAt := capturedAt.UTC()
	event := bson.M{
		"student_id":       verdict.StudentID,
		"class_id":         eventClass,
		"device_id":        deviceID,
		"scanned_at":       scannedAt.Format(time.RFC3339),
		"face_verified":    faceVerified,
		"liveness_passed":  livenessPassed,
		"confidence":       verdict.Confidence,
		"status":           status,
		"review_reason":    reason,
		"frames_total":     verdict.FramesTotal,
		"frames_with_face": verdict.FramesWithFace,
		"frames_matched":   verdict.FramesMatched,
	}
	if s.images != nil {
		evidence := frames[verdict.BestFrame]
		ext, contentType := imageExtension(evidence)
		if key := s.uploadImage(ctx, scanImageKey(deviceID, scannedAt, ext), contentType, evidence); key != "" {
			event["image_key"] = key
		}
	}
	if _, err = s.db.Collection("scan_events").InsertOne(ctx, event); err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal menyimpan event scan.")
		return
	}
	_, _ = s.db.Collection("devices").UpdateOne(ctx,
		bson.M{"_id": deviceID},
		bson.M{"$set": bson.M{"last_seen": time.Now().UTC().Format(time.RFC3339)}},
	)
	log.Printf("scan %s: %s (%s) frame=%d wajah=%d cocok=%d skor=%.2f dalam %s",
		deviceID, status, result, verdict.FramesTotal, verdict.FramesWithFace, verdict.FramesMatched,
		verdict.Confidence, time.Since(started).Round(time.Millisecond))

	var studentJSON map[string]any
	if faceVerified {
		studentJSON = map[string]any{"id": verdict.StudentID, "nim": student.NIM, "name": student.Name}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": status, "result": result, "message": scanDisplayMessage(result, reason),
		"student": studentJSON, "classId": eventClass, "faceVerified": faceVerified,
		"confidence": verdict.Confidence, "reviewReason": reason,
		"framesTotal": verdict.FramesTotal, "framesWithFace": verdict.FramesWithFace, "framesMatched": verdict.FramesMatched,
	})
}
