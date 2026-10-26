package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"dash.algorithmics/internal/storage"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Penyimpanan foto wajah di object storage S3 (Garage).
//
// Matching hanya memakai embedding; foto disimpan untuk tiga keperluan:
// enrollment ulang otomatis saat model diganti, peninjauan scan "Perlu
// ditinjau", dan audit/sanggahan. Gambar tidak pernah masuk MongoDB; MongoDB
// hanya menyimpan kunci objeknya di field image_key (face_templates dan
// scan_events). Bila storage tidak dikonfigurasi atau sedang mati, enrollment
// dan scan tetap berjalan tanpa foto.

// ImageStore adalah operasi object storage yang dipakai server.
type ImageStore interface {
	Put(ctx context.Context, key, contentType string, data []byte) error
	Get(ctx context.Context, key string) (io.ReadCloser, string, int64, error)
	Delete(ctx context.Context, key string) error
	HeadBucket(ctx context.Context) error
}

// SetImageStore mengaktifkan penyimpanan foto. nil menonaktifkan.
func (s *Server) SetImageStore(store ImageStore) { s.images = store }

const imageUploadTimeout = 3 * time.Second

// imageExtension menebak tipe gambar dari isinya (bukan dari nama file).
func imageExtension(data []byte) (string, string) {
	switch http.DetectContentType(data) {
	case "image/png":
		return "png", "image/png"
	case "image/webp":
		return "webp", "image/webp"
	default:
		return "jpg", "image/jpeg"
	}
}

func randomSuffix() string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// enrollImageKey: enroll/{student_id}/{waktu}-{acak}.{ext}
func enrollImageKey(studentID string, at time.Time, ext string) string {
	return "enroll/" + studentID + "/" + at.UTC().Format("20060102T150405Z") + "-" + randomSuffix() + "." + ext
}

// scanImageKey: scans/{yyyy}/{mm}/{dd}/{waktu}-{device}-{acak}.{ext}
func scanImageKey(deviceID string, at time.Time, ext string) string {
	at = at.UTC()
	return "scans/" + at.Format("2006/01/02") + "/" + at.Format("150405Z") + "-" + safeKeyPart(deviceID) + "-" + randomSuffix() + "." + ext
}

func safeKeyPart(value string) string {
	var out strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	if out.Len() == 0 {
		return "device"
	}
	return out.String()
}

// uploadImage mengunggah foto dengan batas waktu pendek. Gagal unggah
// dicatat di log dan mengembalikan string kosong, bukan error, agar absensi
// dan enrollment tidak gagal hanya karena penyimpanan foto bermasalah.
func (s *Server) uploadImage(ctx context.Context, key, contentType string, data []byte) string {
	if s.images == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), imageUploadTimeout)
	defer cancel()
	if err := s.images.Put(ctx, key, contentType, data); err != nil {
		log.Printf("image storage: upload %s gagal: %v", key, err)
		return ""
	}
	return key
}

func (s *Server) deleteImage(ctx context.Context, key string) {
	if s.images == nil || key == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), imageUploadTimeout)
	defer cancel()
	if err := s.images.Delete(ctx, key); err != nil {
		log.Printf("image storage: hapus %s gagal: %v", key, err)
	}
}

// storeEnrollmentImage menyimpan foto enrollment lalu mengganti image_key pada
// template; foto lama dihapus agar tiap mahasiswa hanya punya satu foto.
func (s *Server) storeEnrollmentImage(ctx context.Context, studentID string, image []byte) string {
	if s.images == nil {
		return ""
	}
	var previous struct {
		ImageKey string `bson:"image_key"`
	}
	_ = s.db.Collection("face_templates").FindOne(ctx, bson.M{"student_id": studentID},
		options.FindOne().SetProjection(bson.M{"image_key": 1})).Decode(&previous)

	ext, contentType := imageExtension(image)
	key := s.uploadImage(ctx, enrollImageKey(studentID, time.Now(), ext), contentType, image)
	if key == "" {
		return ""
	}
	_, err := s.db.Collection("face_templates").UpdateOne(ctx, bson.M{"student_id": studentID},
		bson.M{"$set": bson.M{"image_key": key}})
	if err != nil {
		log.Printf("image storage: simpan image_key %s gagal: %v", key, err)
		s.deleteImage(ctx, key)
		return ""
	}
	if previous.ImageKey != "" && previous.ImageKey != key {
		s.deleteImage(ctx, previous.ImageKey)
	}
	return key
}

// ErrImageForbidden dan ErrImageNotFound dipakai serveImage.
var (
	ErrImageNotFound  = errors.New("image not found")
	ErrImageForbidden = errors.New("image forbidden")
)

// authorizeImage memastikan kunci foto tercatat di MongoDB dan boleh dilihat
// pengguna: administrator semua foto, dosen hanya foto dari kelasnya.
func (s *Server) authorizeImage(ctx context.Context, user principal, key string) error {
	var classID string
	switch {
	case strings.HasPrefix(key, "scans/"):
		var event struct {
			ClassID string `bson:"class_id"`
		}
		if err := s.db.Collection("scan_events").FindOne(ctx, bson.M{"image_key": key}).Decode(&event); err != nil {
			return ErrImageNotFound
		}
		classID = event.ClassID
	case strings.HasPrefix(key, "enroll/"):
		var template struct {
			StudentID string `bson:"student_id"`
		}
		if err := s.db.Collection("face_templates").FindOne(ctx, bson.M{"image_key": key}).Decode(&template); err != nil {
			return ErrImageNotFound
		}
		var student struct {
			ClassID string `bson:"class_id"`
		}
		if err := s.db.Collection("students").FindOne(ctx, bson.M{"_id": template.StudentID}).Decode(&student); err != nil {
			return ErrImageNotFound
		}
		classID = student.ClassID
	default:
		return ErrImageNotFound
	}
	switch user.role {
	case "administrator":
		return nil
	case "dosen":
		if slices.Contains(user.classes, classID) {
			return nil
		}
	}
	return ErrImageForbidden
}

// serveImage: GET /api/v1/images/{key...} (sesi staf). Foto dialirkan lewat
// Go sehingga browser tidak perlu akses langsung ke Garage.
func (s *Server) serveImage(w http.ResponseWriter, r *http.Request) {
	user, ok := principalFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesi tidak valid.")
		return
	}
	if s.images == nil {
		writeError(w, http.StatusNotFound, "image_storage_disabled", "Penyimpanan foto belum dikonfigurasi.")
		return
	}
	key := r.PathValue("key")
	if key == "" || strings.Contains(key, "..") {
		writeError(w, http.StatusNotFound, "image_not_found", "Foto tidak ditemukan.")
		return
	}
	switch err := s.authorizeImage(r.Context(), user, key); {
	case errors.Is(err, ErrImageForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "Anda tidak berhak melihat foto ini.")
		return
	case err != nil:
		writeError(w, http.StatusNotFound, "image_not_found", "Foto tidak ditemukan.")
		return
	}
	body, contentType, size, err := s.images.Get(r.Context(), key)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "image_not_found", "Foto sudah dihapus dari penyimpanan.")
		return
	}
	if err != nil {
		log.Printf("image storage: ambil %s gagal: %v", key, err)
		writeError(w, http.StatusBadGateway, "image_storage_error", "Penyimpanan foto tidak dapat dihubungi.")
		return
	}
	defer body.Close()
	if !strings.HasPrefix(contentType, "image/") {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", "inline")
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	_, _ = io.Copy(w, body)
}

// PurgeExpiredImages menghapus foto scan yang melewati masa retensi lalu
// menghapus image_key dari event-nya. Event-nya sendiri tetap ada.
// Retensi <= 0 berarti foto status itu disimpan tanpa batas.
func (s *Server) PurgeExpiredImages(ctx context.Context) (int, error) {
	if s.images == nil {
		return 0, nil
	}
	rules := []struct {
		statuses []string
		days     int
	}{
		{[]string{"Hadir"}, s.config.ImageRetentionDays},
		{[]string{"Perlu ditinjau", "Ditolak"}, s.config.ImageReviewRetentionDays},
	}
	removed := 0
	for _, rule := range rules {
		if rule.days <= 0 {
			continue
		}
		cutoff := time.Now().UTC().AddDate(0, 0, -rule.days).Format(time.RFC3339)
		cursor, err := s.db.Collection("scan_events").Find(ctx, bson.M{
			"status":     bson.M{"$in": rule.statuses},
			"image_key":  bson.M{"$exists": true, "$ne": ""},
			"scanned_at": bson.M{"$lt": cutoff},
		}, options.Find().SetProjection(bson.M{"image_key": 1}).SetLimit(500))
		if err != nil {
			return removed, err
		}
		var events []struct {
			ID       any    `bson:"_id"`
			ImageKey string `bson:"image_key"`
		}
		if err := cursor.All(ctx, &events); err != nil {
			return removed, err
		}
		for _, event := range events {
			if err := s.images.Delete(ctx, event.ImageKey); err != nil {
				log.Printf("image storage: purge %s gagal: %v", event.ImageKey, err)
				continue
			}
			if _, err := s.db.Collection("scan_events").UpdateOne(ctx, bson.M{"_id": event.ID},
				bson.M{"$unset": bson.M{"image_key": ""}}); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

// RunImageRetention menjalankan PurgeExpiredImages saat start lalu tiap
// interval sampai ctx selesai.
func (s *Server) RunImageRetention(ctx context.Context, interval time.Duration) {
	if s.images == nil {
		return
	}
	run := func() {
		if removed, err := s.PurgeExpiredImages(ctx); err != nil {
			log.Printf("image retention: %v", err)
		} else if removed > 0 {
			log.Printf("image retention: %d foto scan kedaluwarsa dihapus", removed)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// imageStorageHealth dipakai /api/v1/health.
func (s *Server) imageStorageHealth(ctx context.Context) map[string]any {
	if s.images == nil {
		return map[string]any{"enabled": false, "online": false}
	}
	ctx, cancel := context.WithTimeout(ctx, imageUploadTimeout)
	defer cancel()
	return map[string]any{"enabled": true, "online": s.images.HeadBucket(ctx) == nil}
}

// ImageURL mengubah image_key menjadi URL dashboard (kosong bila tidak ada).
func ImageURL(key string) string {
	if key == "" {
		return ""
	}
	return "/api/v1/images/" + key
}
