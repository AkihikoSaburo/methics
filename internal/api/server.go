package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"dash.algorithmics/internal/security"
	"dash.algorithmics/internal/storage"
	"dash.algorithmics/internal/store"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Config struct {
	Environment      string
	FaceServiceURL   string
	FaceServiceToken string
	DeviceToken      string
	// SimThreshold adalah ambang cosine similarity ArcFace untuk menyatakan
	// wajah cocok (FACE_SIM_THRESHOLD, default 0.45 = SIM_THRESHOLD face service).
	SimThreshold float64
	// PoseThreshold adalah ambang kemiripan antar-foto dalam satu enrollment
	// (FACE_POSE_THRESHOLD, default 0.30). Lebih longgar dari SimThreshold
	// karena foto kiri/kanan/atas/bawah wajar kurang mirip dengan foto depan,
	// tetapi tetap jauh di atas kemiripan dua orang berbeda.
	PoseThreshold float64
	// ScanMinMatchFrames: jumlah frame minimal yang cocok dengan mahasiswa
	// yang sama agar scan langsung Hadir tanpa bukti liveness dari perangkat
	// (SCAN_MIN_MATCH_FRAMES, default 2). Lihat scan.go.
	ScanMinMatchFrames int
	// FaceServiceTimeout membatasi lama satu panggilan /embed; /register
	// mendapat FaceServiceTimeout × jumlah foto.
	FaceServiceTimeout time.Duration
	// ImageStorage adalah koneksi S3 (Garage) untuk foto wajah; kosong =
	// foto tidak disimpan.
	ImageStorage storage.Config
	// ImageRetentionDays: masa simpan foto scan berstatus Hadir (hari).
	ImageRetentionDays int
	// ImageReviewRetentionDays: masa simpan foto scan Perlu ditinjau/Ditolak.
	ImageReviewRetentionDays int
}

type Server struct {
	db     *store.Database
	config Config
	client *http.Client
	images ImageStore
}

type principal struct {
	role    string
	nim     string
	classes []string
}

// ClassRow adalah ringkasan satu kelas. Dipakai bersama oleh endpoint JSON
// dan fragment HTML (htmx) agar query tetap satu sumber.
type ClassRow struct {
	ID             string
	Name           string
	StudentCount   int
	Enrolled       int
	Present        int
	AttendanceRate int
}

// ScanEvent adalah satu baris riwayat scan. Dipakai bersama endpoint JSON dan
// fragment HTML agar formatting/field tetap konsisten.
type ScanEvent struct {
	StudentNIM   string
	StudentName  string
	ClassID      string
	ClassName    string
	ScannedAt    string
	Status       string
	Confidence   float64
	ReviewReason string
	DeviceID     string
	// ImageURL menunjuk foto scan di GET /api/v1/images/...; kosong bila
	// foto tidak disimpan atau sudah melewati masa retensi.
	ImageURL string
}

func New(db *store.Database, config Config) *Server {
	if config.SimThreshold <= 0 || config.SimThreshold > 1 {
		config.SimThreshold = 0.45
	}
	if config.PoseThreshold <= 0 || config.PoseThreshold > 1 {
		config.PoseThreshold = 0.30
	}
	if config.FaceServiceTimeout <= 0 {
		config.FaceServiceTimeout = 15 * time.Second
	}
	if config.ScanMinMatchFrames <= 0 {
		config.ScanMinMatchFrames = 2
	}
	// Batas waktu diatur per panggilan lewat context (lihat face.go), karena
	// enrollment 6 foto butuh waktu lebih lama daripada satu scan.
	return &Server{
		db:     db,
		config: config,
		client: &http.Client{},
	}
}

func (s *Server) StaffHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/dashboard", s.dashboard)
	mux.HandleFunc("GET /api/v1/classes", s.classes)
	mux.HandleFunc("GET /api/v1/reports", s.reports)
	mux.HandleFunc("POST /api/v1/students", s.createStudent)
	mux.HandleFunc("POST /api/v1/students/{nim}/face-template", s.enrollFace)
	mux.HandleFunc("DELETE /api/v1/students/{nim}", s.deleteStudent)
	mux.HandleFunc("GET /api/v1/staff", s.listStaff)
	mux.HandleFunc("POST /api/v1/staff", s.createLecturer)
	mux.HandleFunc("DELETE /api/v1/staff/{id}", s.deleteStaff)
	mux.HandleFunc("GET /api/v1/images/{key...}", s.serveImage)
	return mux
}

func (s *Server) DeviceHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/devices/ping", s.devicePing)
	mux.HandleFunc("POST /api/v1/devices/ping", s.devicePing)
	mux.HandleFunc("POST /api/v1/scans", s.receiveScan)
	return mux
}

func (s *Server) StaticHandler(staticDir string) http.Handler {
	return http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir)))
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	user, ok := principalFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Autentikasi pengguna diperlukan.")
		return
	}

	ctx := r.Context()
	classes, err := s.dashboardClasses(ctx, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal memuat data kelas.")
		return
	}
	attendance, err := s.dashboardAttendance(ctx, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal memuat roster mahasiswa.")
		return
	}
	students, err := s.countStudents(ctx, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal menghitung mahasiswa.")
		return
	}
	verified, review, err := s.countScans(ctx, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal menghitung hasil scan.")
		return
	}
	templates, err := s.countTemplates(ctx, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal menghitung profil wajah.")
		return
	}
	lastSeen, err := s.lastDeviceSeen(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal membaca status perangkat.")
		return
	}
	face := s.faceHealth(ctx)
	deviceOnline := lastSeen != nil && time.Since(*lastSeen) < 2*time.Minute

	writeJSON(w, http.StatusOK, map[string]any{
		"summary": map[string]any{
			"students": students, "classes": len(classes), "verifiedScans": verified,
			"scansToReview": review, "faceTemplates": templates,
		},
		"classes": classes,
		"integrations": map[string]any{
			"esp32":            map[string]any{"online": deviceOnline, "lastSeen": formatTime(lastSeen)},
			"faceVerification": face,
		},
		"attendance": rosterToMaps(attendance),
	})
}

func (s *Server) classes(w http.ResponseWriter, r *http.Request) {
	user, ok := principalFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Autentikasi pengguna diperlukan.")
		return
	}
	ctx := r.Context()
	overview, err := s.classOverview(ctx, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal memuat data kelas.")
		return
	}
	students, err := s.countStudents(ctx, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal menghitung mahasiswa.")
		return
	}
	enrolled, err := s.countTemplates(ctx, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal menghitung profil wajah.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": map[string]any{
			"classes": len(overview), "students": students, "enrolled": enrolled, "present": sumPresent(overview),
		},
		"classes": classRowsToMaps(overview),
	})
}

func (s *Server) reports(w http.ResponseWriter, r *http.Request) {
	user, ok := principalFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Autentikasi pengguna diperlukan.")
		return
	}
	ctx := r.Context()
	overview, err := s.classOverview(ctx, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal memuat data kelas.")
		return
	}
	statuses, err := s.scanStatusCounts(ctx, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal menghitung status scan.")
		return
	}
	events, err := s.recentEvents(ctx, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal memuat riwayat scan.")
		return
	}
	students, err := s.countStudents(ctx, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal menghitung mahasiswa.")
		return
	}
	present := sumPresent(overview)
	rate := 0
	if students > 0 {
		rate = present * 100 / students
	}
	breakdown := make([]map[string]any, 0, 3)
	for _, status := range []string{"Hadir", "Perlu ditinjau", "Ditolak"} {
		if count := statuses[status]; count > 0 {
			breakdown = append(breakdown, map[string]any{"status": status, "count": count})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": map[string]any{
			"verifiedScans": statuses["Hadir"], "reviewScans": statuses["Perlu ditinjau"],
			"rejectedScans": statuses["Ditolak"], "students": students, "present": present, "attendanceRate": rate,
		},
		"statusBreakdown": breakdown,
		"classes":         classRowsToMaps(overview),
		"events":          scanEventsToMaps(events),
	})
}

func todayRangeUTC() (string, string) {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UTC()
	end := start.Add(24 * time.Hour)
	return start.Format(time.RFC3339), end.Format(time.RFC3339)
}

func (s *Server) classOverview(ctx context.Context, user principal) ([]ClassRow, error) {
	classFilter := bson.M{}
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return []ClassRow{}, nil
		}
		classFilter["_id"] = bson.M{"$in": user.classes}
	}
	cur, err := s.db.Collection("classes").Find(ctx, classFilter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var classes []struct {
		ID   string `bson:"_id"`
		Name string `bson:"name"`
	}
	if err := cur.All(ctx, &classes); err != nil {
		return nil, err
	}

	startStr, endStr := todayRangeUTC()

	result := make([]ClassRow, 0, len(classes))
	for _, c := range classes {
		studentCount, err := s.db.Collection("students").CountDocuments(ctx, bson.M{"class_id": c.ID})
		if err != nil {
			return nil, err
		}

		sCur, err := s.db.Collection("students").Find(ctx, bson.M{"class_id": c.ID}, options.Find().SetProjection(bson.M{"_id": 1}))
		var studentIDs []string
		if err == nil {
			var stDocs []struct {
				ID string `bson:"_id"`
			}
			if err := sCur.All(ctx, &stDocs); err == nil {
				for _, st := range stDocs {
					studentIDs = append(studentIDs, st.ID)
				}
			}
		}

		var enrolledCount int64
		if len(studentIDs) > 0 {
			enrolledCount, _ = s.db.Collection("face_templates").CountDocuments(ctx, bson.M{"student_id": bson.M{"$in": studentIDs}})
		}

		distinctPresent, err := s.db.Collection("scan_events").Distinct(ctx, "student_id", bson.M{
			"class_id":   c.ID,
			"status":     "Hadir",
			"scanned_at": bson.M{"$gte": startStr, "$lt": endStr},
		})
		presentCount := 0
		if err == nil {
			for _, val := range distinctPresent {
				if sID, ok := val.(string); ok && sID != "" {
					presentCount++
				}
			}
		}

		row := ClassRow{
			ID:           c.ID,
			Name:         c.Name,
			StudentCount: int(studentCount),
			Enrolled:     int(enrolledCount),
			Present:      presentCount,
		}
		if row.StudentCount > 0 {
			row.AttendanceRate = row.Present * 100 / row.StudentCount
		}
		result = append(result, row)
	}
	return result, nil
}

func (s *Server) scanStatusCounts(ctx context.Context, user principal) (map[string]int, error) {
	startStr, endStr := todayRangeUTC()
	filter := bson.M{
		"scanned_at": bson.M{"$gte": startStr, "$lt": endStr},
	}
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return map[string]int{}, nil
		}
		filter["class_id"] = bson.M{"$in": user.classes}
	}
	return s.aggregateStatusCounts(ctx, filter)
}

func (s *Server) aggregateStatusCounts(ctx context.Context, filter bson.M) (map[string]int, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: filter}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$status"},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
		}}},
	}
	cur, err := s.db.Collection("scan_events").Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	counts := make(map[string]int)
	for cur.Next(ctx) {
		var row struct {
			Status string `bson:"_id"`
			Count  int    `bson:"count"`
		}
		if err := cur.Decode(&row); err == nil {
			counts[row.Status] = row.Count
		}
	}
	return counts, cur.Err()
}

func (s *Server) recentEvents(ctx context.Context, user principal) ([]ScanEvent, error) {
	filter := bson.M{}
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return []ScanEvent{}, nil
		}
		filter["class_id"] = bson.M{"$in": user.classes}
	}
	return s.fetchScanEvents(ctx, filter, 50)
}

func sumPresent(classes []ClassRow) int {
	total := 0
	for _, item := range classes {
		total += item.Present
	}
	return total
}

func classRowsToMaps(rows []ClassRow) []map[string]any {
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		result = append(result, map[string]any{
			"id": row.ID, "name": row.Name, "studentCount": row.StudentCount,
			"enrolled": row.Enrolled, "present": row.Present, "attendanceRate": row.AttendanceRate,
		})
	}
	return result
}

// ClassRows, StudentCount, dan TemplateCount mengekspos query ber-scope role
// agar fragment HTML (htmx) dapat memakai ulang logika yang sama tanpa duplikasi.
func (s *Server) ClassRows(ctx context.Context, role string, classes []string) ([]ClassRow, error) {
	return s.classOverview(ctx, principal{role: role, classes: classes})
}

func (s *Server) StudentCount(ctx context.Context, role string, classes []string) (int, error) {
	return s.countStudents(ctx, principal{role: role, classes: classes})
}

func (s *Server) TemplateCount(ctx context.Context, role string, classes []string) (int, error) {
	return s.countTemplates(ctx, principal{role: role, classes: classes})
}

func (s *Server) RecentEvents(ctx context.Context, role string, classes []string) ([]ScanEvent, error) {
	return s.recentEvents(ctx, principal{role: role, classes: classes})
}

func (s *Server) ScanStatusCounts(ctx context.Context, role string, classes []string) (map[string]int, error) {
	return s.scanStatusCounts(ctx, principal{role: role, classes: classes})
}

// AllScanEvents mengembalikan event scan seluruh waktu (200 terbaru) untuk
// halaman Log, dengan filter opsional status, kelas, dan pencarian nama/NIM.
// Filter kelas dari dosen yang bukan kelasnya menghasilkan daftar kosong, jadi
// filter pengguna tidak pernah melonggarkan cakupan role.
func (s *Server) AllScanEvents(ctx context.Context, role string, roleClasses []string, statusFilter, classFilter, search string) ([]ScanEvent, error) {
	filter := bson.M{}
	if role == "dosen" {
		if len(roleClasses) == 0 {
			return []ScanEvent{}, nil
		}
		filter["class_id"] = bson.M{"$in": roleClasses}
	}
	if statusFilter != "" && statusFilter != "all" {
		filter["status"] = statusFilter
	}
	if classFilter != "" && classFilter != "all" {
		classFilter = strings.ToUpper(classFilter)
		if role == "dosen" && !slices.Contains(roleClasses, classFilter) {
			return []ScanEvent{}, nil
		}
		filter["class_id"] = classFilter
	}

	search = strings.TrimSpace(search)
	if search != "" {
		needle := regexp.QuoteMeta(search)
		studentCur, err := s.db.Collection("students").Find(ctx, bson.M{
			"$or": []bson.M{
				{"name": bson.M{"$regex": needle, "$options": "i"}},
				{"nim": bson.M{"$regex": needle, "$options": "i"}},
			},
		}, options.Find().SetProjection(bson.M{"_id": 1}))
		if err != nil {
			return nil, err
		}
		var matched []struct {
			ID string `bson:"_id"`
		}
		_ = studentCur.All(ctx, &matched)
		if len(matched) == 0 {
			return []ScanEvent{}, nil
		}
		ids := make([]string, 0, len(matched))
		for _, m := range matched {
			ids = append(ids, m.ID)
		}
		filter["student_id"] = bson.M{"$in": ids}
	}

	return s.fetchScanEvents(ctx, filter, 200)
}

func (s *Server) fetchScanEvents(ctx context.Context, filter bson.M, limit int64) ([]ScanEvent, error) {
	findOpts := options.Find().SetSort(bson.D{{Key: "scanned_at", Value: -1}}).SetLimit(limit)
	cur, err := s.db.Collection("scan_events").Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	type rawScanDoc struct {
		StudentID    string  `bson:"student_id"`
		ClassID      string  `bson:"class_id"`
		DeviceID     string  `bson:"device_id"`
		ScannedAt    string  `bson:"scanned_at"`
		Status       string  `bson:"status"`
		Confidence   float64 `bson:"confidence"`
		ReviewReason string  `bson:"review_reason"`
		ImageKey     string  `bson:"image_key"`
	}
	var docs []rawScanDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return []ScanEvent{}, nil
	}

	studentIDSet := make(map[string]struct{})
	classIDSet := make(map[string]struct{})
	for _, doc := range docs {
		if doc.StudentID != "" {
			studentIDSet[doc.StudentID] = struct{}{}
		}
		if doc.ClassID != "" {
			classIDSet[doc.ClassID] = struct{}{}
		}
	}

	studentIDs := make([]string, 0, len(studentIDSet))
	for id := range studentIDSet {
		studentIDs = append(studentIDs, id)
	}
	classIDs := make([]string, 0, len(classIDSet))
	for id := range classIDSet {
		classIDs = append(classIDs, id)
	}

	studentsMap := make(map[string]struct{ NIM, Name string })
	if len(studentIDs) > 0 {
		stCur, err := s.db.Collection("students").Find(ctx, bson.M{"_id": bson.M{"$in": studentIDs}})
		if err == nil {
			var stList []struct {
				ID   string `bson:"_id"`
				NIM  string `bson:"nim"`
				Name string `bson:"name"`
			}
			if err := stCur.All(ctx, &stList); err == nil {
				for _, st := range stList {
					studentsMap[st.ID] = struct{ NIM, Name string }{NIM: st.NIM, Name: st.Name}
				}
			}
		}
	}

	classesMap := make(map[string]string)
	if len(classIDs) > 0 {
		cCur, err := s.db.Collection("classes").Find(ctx, bson.M{"_id": bson.M{"$in": classIDs}})
		if err == nil {
			var cList []struct {
				ID   string `bson:"_id"`
				Name string `bson:"name"`
			}
			if err := cCur.All(ctx, &cList); err == nil {
				for _, c := range cList {
					classesMap[c.ID] = c.Name
				}
			}
		}
	}

	result := make([]ScanEvent, 0, len(docs))
	for _, doc := range docs {
		st := studentsMap[doc.StudentID]
		className := classesMap[doc.ClassID]
		if className == "" {
			className = doc.ClassID
		}
		result = append(result, ScanEvent{
			StudentNIM:   st.NIM,
			StudentName:  st.Name,
			ClassID:      doc.ClassID,
			ClassName:    className,
			ScannedAt:    doc.ScannedAt,
			Status:       doc.Status,
			Confidence:   doc.Confidence,
			ReviewReason: doc.ReviewReason,
			DeviceID:     doc.DeviceID,
			ImageURL:     ImageURL(doc.ImageKey),
		})
	}
	return result, nil
}

// AllScanStatusCounts menghitung status scan seluruh waktu (tanpa batas tanggal),
// ber-scope role.
func (s *Server) AllScanStatusCounts(ctx context.Context, role string, classes []string) (map[string]int, error) {
	filter := bson.M{}
	if role == "dosen" {
		if len(classes) == 0 {
			return map[string]int{}, nil
		}
		filter["class_id"] = bson.M{"$in": classes}
	}
	return s.aggregateStatusCounts(ctx, filter)
}

// DeviceStatus melaporkan apakah ada perangkat dengan last_seen < 2 menit
// (scan, heartbeat, atau health) dan jam terakhirnya dalam format lokal HH:MM.
func (s *Server) DeviceStatus(ctx context.Context) (bool, string, error) {
	lastSeen, err := s.lastDeviceSeen(ctx)
	if err != nil {
		return false, "", err
	}
	return lastSeen != nil && time.Since(*lastSeen) < 2*time.Minute, formatTime(lastSeen), nil
}

// FaceStatus melaporkan kesehatan layanan verifikasi wajah (online, provider).
func (s *Server) FaceStatus(ctx context.Context) (bool, string) {
	face := s.faceHealth(ctx)
	online, _ := face["online"].(bool)
	provider, _ := face["provider"].(string)
	return online, provider
}

func scanEventsToMaps(events []ScanEvent) []map[string]any {
	result := make([]map[string]any, 0, len(events))
	for _, event := range events {
		result = append(result, map[string]any{
			"student":      map[string]any{"nim": event.StudentNIM, "name": event.StudentName},
			"class":        map[string]any{"id": event.ClassID, "name": event.ClassName},
			"scannedAt":    event.ScannedAt,
			"status":       event.Status,
			"confidence":   event.Confidence,
			"reviewReason": event.ReviewReason,
			"deviceId":     event.DeviceID,
			"imageUrl":     event.ImageURL,
		})
	}
	return result
}

// RosterEntry adalah satu baris roster mahasiswa: identitas, kelas, dan status
// scan terakhir hari ini. Dipakai endpoint JSON dashboard dan fragment /mahasiswa.
type RosterEntry struct {
	StudentID     string
	StudentNIM    string
	StudentName   string
	ClassID       string
	ClassName     string
	ScannedAt     *string
	FaceVerified  bool
	FaceEnrolled  bool
	FaceImageURL  string   // URL foto enrollment pertama (kosong bila tidak tersimpan di Garage)
	FaceImageURLs []string // URL semua foto enrollment (1–6)
	FaceTemplates int      // jumlah embedding pose yang tersimpan (0 = belum enrollment)
	DeviceID      string
	Status        string
}

var (
	ErrMissingFields   = errors.New("missing fields")
	ErrStudentExists   = errors.New("student exists")
	ErrInvalidNIM      = errors.New("invalid nim")
	ErrInvalidClass    = errors.New("invalid class")
	ErrStudentNotFound = errors.New("student not found")
	ErrFaceService     = errors.New("face service error")
)

// Roster mengembalikan roster mahasiswa ber-scope role untuk fragment htmx.
func (s *Server) Roster(ctx context.Context, role string, classes []string) ([]RosterEntry, error) {
	return s.dashboardAttendance(ctx, principal{role: role, classes: classes})
}

// nimPattern: NIM berupa 9 digit angka, mis. 241011001 (2 digit angkatan +
// 4 digit kode prodi + 3 digit nomor urut).
var nimPattern = regexp.MustCompile(`^[0-9]{9}$`)

// ValidNIM melaporkan apakah nim sesuai format 9 digit angka.
func ValidNIM(nim string) bool { return nimPattern.MatchString(nim) }

// CreateStudent memvalidasi dan menyimpan mahasiswa baru. Dipakai endpoint JSON
// dan fragment htmx agar aturan validasi tetap satu sumber.
func (s *Server) CreateStudent(ctx context.Context, name, nim, classID string) (string, error) {
	name = strings.TrimSpace(name)
	nim = strings.TrimSpace(strings.ToUpper(nim))
	classID = strings.TrimSpace(strings.ToUpper(classID))
	if name == "" || nim == "" || classID == "" {
		return "", ErrMissingFields
	}
	if !ValidNIM(nim) {
		return "", ErrInvalidNIM
	}

	classCount, err := s.db.Collection("classes").CountDocuments(ctx, bson.M{"_id": classID})
	if err != nil || classCount == 0 {
		return "", ErrInvalidClass
	}

	existingCount, err := s.db.Collection("students").CountDocuments(ctx, bson.M{"nim": nim})
	if err != nil || existingCount > 0 {
		return "", ErrStudentExists
	}

	id := "student-" + strings.ToLower(nim)
	_, err = s.db.Collection("students").InsertOne(ctx, bson.M{
		"_id":      id,
		"nim":      nim,
		"name":     name,
		"email":    "",
		"class_id": classID,
	})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return "", ErrStudentExists
		}
		return "", err
	}
	_, _ = s.db.Collection("class_students").InsertOne(ctx, bson.M{
		"class_id":   classID,
		"student_id": id,
	})
	return id, nil
}

// EnrollFace mengirim 1–6 foto enrollment (pose berbeda) ke face service
// (POST /register), memvalidasi hasilnya, lalu menyimpan semua embedding
// sebagai template mahasiswa dengan NIM tersebut. Bila penyimpanan foto aktif,
// foto-fotonya juga disimpan ke Garage (result["imageStored"]); kegagalan
// foto tidak menggagalkan enrollment.
// Error yang mungkin: ErrStudentNotFound, ErrInvalidImage, ErrTooManyPhotos,
// ErrNoFace, ErrInconsistentFaces, ErrFaceAlreadyEnrolled (map berisi
// conflictNim), ErrFaceService. Error per foto dibungkus *PhotoError
// (lihat PhotoNumber).
func (s *Server) EnrollFace(ctx context.Context, nim string, images []EnrollImage) (map[string]any, error) {
	nim = strings.ToUpper(strings.TrimSpace(nim))
	var student struct {
		ID string `bson:"_id"`
	}
	err := s.db.Collection("students").FindOne(ctx, bson.M{"nim": nim}).Decode(&student)
	if err != nil {
		return nil, ErrStudentNotFound
	}
	started := time.Now()
	result, err := s.enrollEmbeddings(ctx, student.ID, images)
	if err != nil {
		log.Printf("enrollment %s gagal (%d foto, %s): %v %v", nim, len(images), time.Since(started).Round(time.Millisecond), err, result)
	} else {
		log.Printf("enrollment %s berhasil: %d foto dalam %s", nim, len(images), time.Since(started).Round(time.Millisecond))
	}
	if errors.Is(err, ErrFaceAlreadyEnrolled) {
		if otherID, ok := result["conflictStudentId"].(string); ok {
			var other struct {
				NIM string `bson:"nim"`
			}
			if s.db.Collection("students").FindOne(ctx, bson.M{"_id": otherID}).Decode(&other) == nil {
				result["conflictNim"] = other.NIM
			}
		}
		return result, err
	}
	if err != nil {
		return result, err
	}
	if s.images != nil {
		keys := s.storeEnrollmentImages(ctx, student.ID, images)
		result["imageStored"] = len(keys) > 0
		if len(keys) > 0 {
			urls := make([]string, len(keys))
			for i, key := range keys {
				urls[i] = ImageURL(key)
			}
			result["imageUrls"] = urls
		}
	}
	return result, nil
}

// EnrollRequestTimeout adalah batas waktu baca/tulis satu request
// enrollment (unggah foto + /register + simpan ke Garage).
const EnrollRequestTimeout = 3 * time.Minute

// ReadEnrollImages membaca 1–6 foto enrollment (masing-masing maks 8 MB) dari
// field multipart "images" (atau "image" untuk klien lama), dipakai endpoint
// JSON dan fragment htmx.
func ReadEnrollImages(w http.ResponseWriter, r *http.Request) ([]EnrollImage, error) {
	// ReadTimeout/WriteTimeout server (15 s) terlalu pendek untuk mengunggah
	// dan memproses 6 foto HP; perpanjang khusus request enrollment.
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(EnrollRequestTimeout)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	r.Body = http.MaxBytesReader(w, r.Body, MaxEnrollPhotos*(8<<20)+(1<<20))
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		return nil, fmt.Errorf("kirim multipart/form-data berisi 1–%d foto (maks 8 MB per foto)", MaxEnrollPhotos)
	}
	headers := append(r.MultipartForm.File["images"], r.MultipartForm.File["image"]...)
	switch {
	case len(headers) == 0:
		return nil, errors.New("pilih minimal 1 foto wajah")
	case len(headers) > MaxEnrollPhotos:
		return nil, fmt.Errorf("maksimal %d foto per enrollment (diterima %d)", MaxEnrollPhotos, len(headers))
	}
	images := make([]EnrollImage, 0, len(headers))
	for i, header := range headers {
		file, err := header.Open()
		if err != nil {
			return nil, fmt.Errorf("foto ke-%d tidak dapat dibaca", i+1)
		}
		data, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
		file.Close()
		if err != nil || len(data) == 0 || len(data) > 8<<20 {
			return nil, fmt.Errorf("foto ke-%d kosong atau melebihi 8 MB", i+1)
		}
		images = append(images, EnrollImage{Data: data, Filename: header.Filename})
	}
	return images, nil
}

func rosterToMaps(entries []RosterEntry) []map[string]any {
	result := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		var scanTime any
		if entry.ScannedAt != nil {
			scanTime = *entry.ScannedAt
		}
		result = append(result, map[string]any{
			"student":       map[string]any{"id": entry.StudentID, "nim": entry.StudentNIM, "name": entry.StudentName},
			"class":         map[string]any{"id": entry.ClassID, "name": entry.ClassName},
			"scannedAt":     scanTime,
			"faceVerified":  entry.FaceVerified,
			"faceEnrolled":  entry.FaceEnrolled,
			"faceImageUrl":  entry.FaceImageURL,
			"faceImageUrls": entry.FaceImageURLs,
			"faceTemplates": entry.FaceTemplates,
			"deviceId":      entry.DeviceID,
			"status":        entry.Status,
		})
	}
	return result
}

func (s *Server) dashboardClasses(ctx context.Context, user principal) ([]map[string]any, error) {
	rows, err := s.classOverview(ctx, user)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		result = append(result, map[string]any{
			"id":           row.ID,
			"name":         row.Name,
			"studentCount": row.StudentCount,
			"present":      row.Present,
		})
	}
	return result, nil
}

func (s *Server) dashboardAttendance(ctx context.Context, user principal) ([]RosterEntry, error) {
	classFilter := bson.M{}
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return []RosterEntry{}, nil
		}
		classFilter["_id"] = bson.M{"$in": user.classes}
	}

	cCur, err := s.db.Collection("classes").Find(ctx, classFilter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cCur.Close(ctx)

	var classes []struct {
		ID   string `bson:"_id"`
		Name string `bson:"name"`
	}
	if err := cCur.All(ctx, &classes); err != nil {
		return nil, err
	}

	classIDs := make([]string, 0, len(classes))
	classesMap := make(map[string]string)
	for _, c := range classes {
		classIDs = append(classIDs, c.ID)
		classesMap[c.ID] = c.Name
	}
	if len(classIDs) == 0 {
		return []RosterEntry{}, nil
	}

	stCur, err := s.db.Collection("students").Find(ctx,
		bson.M{"class_id": bson.M{"$in": classIDs}},
		options.Find().SetSort(bson.D{{Key: "class_id", Value: 1}, {Key: "nim", Value: 1}}),
	)
	if err != nil {
		return nil, err
	}
	defer stCur.Close(ctx)

	var students []struct {
		ID      string `bson:"_id"`
		NIM     string `bson:"nim"`
		Name    string `bson:"name"`
		ClassID string `bson:"class_id"`
	}
	if err := stCur.All(ctx, &students); err != nil {
		return nil, err
	}
	if len(students) == 0 {
		return []RosterEntry{}, nil
	}

	studentIDs := make([]string, 0, len(students))
	for _, st := range students {
		studentIDs = append(studentIDs, st.ID)
	}

	// Embedding 512-d tidak diambil; roster cukup tahu jumlah pose dan kunci foto.
	type faceInfo struct {
		templates int
		imageURLs []string
	}
	faceMap := make(map[string]faceInfo)
	ftCur, err := s.db.Collection("face_templates").Find(ctx, bson.M{"student_id": bson.M{"$in": studentIDs}},
		options.Find().SetProjection(bson.M{"student_id": 1, "image_key": 1, "image_keys": 1, "template_count": 1}))
	if err == nil {
		var ftList []struct {
			StudentID     string   `bson:"student_id"`
			ImageKey      string   `bson:"image_key"`
			ImageKeys     []string `bson:"image_keys"`
			TemplateCount int      `bson:"template_count"`
		}
		if err := ftCur.All(ctx, &ftList); err == nil {
			for _, ft := range ftList {
				info := faceInfo{templates: max(ft.TemplateCount, 1)} // dokumen lama: 1 embedding
				keys := ft.ImageKeys
				if len(keys) == 0 && ft.ImageKey != "" {
					keys = []string{ft.ImageKey}
				}
				for _, key := range keys {
					info.imageURLs = append(info.imageURLs, ImageURL(key))
				}
				faceMap[ft.StudentID] = info
			}
		}
	}

	startStr, endStr := todayRangeUTC()
	scanCur, err := s.db.Collection("scan_events").Find(ctx,
		bson.M{
			"class_id":   bson.M{"$in": classIDs},
			"scanned_at": bson.M{"$gte": startStr, "$lt": endStr},
		},
		options.Find().SetSort(bson.D{{Key: "scanned_at", Value: -1}}),
	)
	type scanDoc struct {
		StudentID    string `bson:"student_id"`
		ClassID      string `bson:"class_id"`
		ScannedAt    string `bson:"scanned_at"`
		FaceVerified bool   `bson:"face_verified"`
		DeviceID     string `bson:"device_id"`
		Status       string `bson:"status"`
	}
	latestScanMap := make(map[string]scanDoc)
	if err == nil {
		var allScans []scanDoc
		if err := scanCur.All(ctx, &allScans); err == nil {
			for _, sc := range allScans {
				key := sc.StudentID + "|" + sc.ClassID
				if _, ok := latestScanMap[key]; !ok {
					latestScanMap[key] = sc
				}
			}
		}
	}

	result := make([]RosterEntry, 0, len(students))
	for _, st := range students {
		entry := RosterEntry{
			StudentID:   st.ID,
			StudentNIM:  st.NIM,
			StudentName: st.Name,
			ClassID:     st.ClassID,
			ClassName:   classesMap[st.ClassID],
			Status:      "Belum hadir",
		}
		if info, ok := faceMap[st.ID]; ok {
			entry.FaceEnrolled = true
			entry.FaceTemplates = info.templates
			entry.FaceImageURLs = info.imageURLs
			if len(info.imageURLs) > 0 {
				entry.FaceImageURL = info.imageURLs[0]
			}
		}
		key := st.ID + "|" + st.ClassID
		if scan, ok := latestScanMap[key]; ok {
			scannedTime := scan.ScannedAt
			entry.ScannedAt = &scannedTime
			if scan.FaceVerified {
				entry.FaceVerified = true
			}
			entry.DeviceID = scan.DeviceID
			if scan.Status != "" {
				entry.Status = scan.Status
			}
		}
		result = append(result, entry)
	}
	return result, nil
}

func (s *Server) countStudents(ctx context.Context, user principal) (int, error) {
	filter := bson.M{}
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return 0, nil
		}
		filter["class_id"] = bson.M{"$in": user.classes}
	}
	count, err := s.db.Collection("students").CountDocuments(ctx, filter)
	return int(count), err
}

func (s *Server) countTemplates(ctx context.Context, user principal) (int, error) {
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return 0, nil
		}
		sCur, err := s.db.Collection("students").Find(ctx, bson.M{"class_id": bson.M{"$in": user.classes}}, options.Find().SetProjection(bson.M{"_id": 1}))
		if err != nil {
			return 0, err
		}
		var stDocs []struct {
			ID string `bson:"_id"`
		}
		_ = sCur.All(ctx, &stDocs)
		if len(stDocs) == 0 {
			return 0, nil
		}
		ids := make([]string, 0, len(stDocs))
		for _, st := range stDocs {
			ids = append(ids, st.ID)
		}
		count, err := s.db.Collection("face_templates").CountDocuments(ctx, bson.M{"student_id": bson.M{"$in": ids}})
		return int(count), err
	}
	count, err := s.db.Collection("face_templates").CountDocuments(ctx, bson.M{})
	return int(count), err
}

func (s *Server) countScans(ctx context.Context, user principal) (int, int, error) {
	startStr, endStr := todayRangeUTC()
	baseFilter := bson.M{
		"scanned_at": bson.M{"$gte": startStr, "$lt": endStr},
	}
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return 0, 0, nil
		}
		baseFilter["class_id"] = bson.M{"$in": user.classes}
	}

	filterVerified := bson.M{}
	for k, v := range baseFilter {
		filterVerified[k] = v
	}
	filterVerified["status"] = "Hadir"
	verified, err := s.db.Collection("scan_events").CountDocuments(ctx, filterVerified)
	if err != nil {
		return 0, 0, err
	}

	filterReview := bson.M{}
	for k, v := range baseFilter {
		filterReview[k] = v
	}
	filterReview["status"] = "Perlu ditinjau"
	review, err := s.db.Collection("scan_events").CountDocuments(ctx, filterReview)
	if err != nil {
		return 0, 0, err
	}

	return int(verified), int(review), nil
}

func (s *Server) createStudent(w http.ResponseWriter, r *http.Request) {
	user, ok := principalFromRequest(r)
	if !ok || user.role != "administrator" {
		writeError(w, http.StatusForbidden, "forbidden", "Hanya administrator yang dapat mendaftarkan mahasiswa.")
		return
	}
	var input struct {
		Name    string `json:"name"`
		NIM     string `json:"nim"`
		ClassID string `json:"classId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Body JSON tidak valid.")
		return
	}
	id, err := s.CreateStudent(r.Context(), input.Name, input.NIM, input.ClassID)
	if err != nil {
		switch {
		case errors.Is(err, ErrMissingFields):
			writeError(w, http.StatusBadRequest, "missing_fields", "name, nim, dan classId wajib diisi.")
		case errors.Is(err, ErrInvalidNIM):
			writeError(w, http.StatusBadRequest, "invalid_nim", "NIM harus 9 digit angka, mis. 241011001.")
		case errors.Is(err, ErrStudentExists):
			writeError(w, http.StatusConflict, "student_exists", "NIM sudah terdaftar atau data tidak valid.")
		case errors.Is(err, ErrInvalidClass):
			writeError(w, http.StatusBadRequest, "invalid_class", "Kelas tidak ditemukan.")
		default:
			writeError(w, http.StatusInternalServerError, "database_error", "Gagal menyimpan mahasiswa.")
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "nim": strings.ToUpper(strings.TrimSpace(input.NIM)),
		"name": strings.TrimSpace(input.Name), "classId": strings.ToUpper(strings.TrimSpace(input.ClassID)), "faceEnrolled": false,
	})
}

func (s *Server) enrollFace(w http.ResponseWriter, r *http.Request) {
	user, ok := principalFromRequest(r)
	if !ok || user.role != "administrator" {
		writeError(w, http.StatusForbidden, "forbidden", "Hanya administrator yang dapat memulai enrollment wajah.")
		return
	}
	nim := strings.ToUpper(strings.TrimSpace(r.PathValue("nim")))
	images, err := ReadEnrollImages(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_image", err.Error())
		return
	}
	response, err := s.EnrollFace(r.Context(), nim, images)
	if err != nil {
		status, code := http.StatusInternalServerError, "enroll_error"
		switch {
		case errors.Is(err, ErrStudentNotFound):
			status, code = http.StatusNotFound, "student_not_found"
		case errors.Is(err, ErrInvalidImage), errors.Is(err, ErrTooManyPhotos):
			status, code = http.StatusBadRequest, "invalid_image"
		case errors.Is(err, ErrNoFace):
			status, code = http.StatusUnprocessableEntity, "no_face"
		case errors.Is(err, ErrInconsistentFaces):
			status, code = http.StatusUnprocessableEntity, "inconsistent_faces"
		case errors.Is(err, ErrFaceAlreadyEnrolled):
			status, code = http.StatusConflict, "face_already_enrolled"
		case errors.Is(err, ErrFaceService):
			status, code = http.StatusBadGateway, "face_service_error"
		}
		writeError(w, status, code, EnrollErrorMessage(err, response))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"nim": nim, "faceEnrolled": true, "result": response})
}

// EnrollErrorMessage menerjemahkan error EnrollFace menjadi pesan berbahasa
// Indonesia (dipakai endpoint JSON dan fragment htmx). detail adalah map
// yang dikembalikan EnrollFace bersama error (boleh nil).
func EnrollErrorMessage(err error, detail map[string]any) string {
	photo := ""
	if n := PhotoNumber(err); n > 0 {
		photo = fmt.Sprintf(" (foto ke-%d)", n)
	}
	switch {
	case errors.Is(err, ErrStudentNotFound):
		return "Mahasiswa tidak ditemukan."
	case errors.Is(err, ErrTooManyPhotos):
		return fmt.Sprintf("Maksimal %d foto per enrollment.", MaxEnrollPhotos)
	case errors.Is(err, ErrInvalidImage):
		return "Gambar tidak dapat dibaca" + photo + "; unggah foto JPEG/PNG yang valid."
	case errors.Is(err, ErrNoFace):
		return "Tidak ada wajah yang terdeteksi" + photo + ". Gunakan foto wajah yang jelas dan terang."
	case errors.Is(err, ErrInconsistentFaces):
		return "Wajah" + photo + " tidak cocok dengan foto pertama. Pastikan semua foto adalah mahasiswa yang sama."
	case errors.Is(err, ErrFaceAlreadyEnrolled):
		conflict, _ := detail["conflictNim"].(string)
		return fmt.Sprintf("Wajah%s sudah terdaftar atas NIM %s; enrollment dibatalkan.", photo, conflict)
	case errors.Is(err, ErrFaceService):
		return "Enrollment gagal; pastikan service Python dan model wajah aktif."
	}
	return "Enrollment gagal."
}

// devicePing: GET|POST /api/v1/devices/ping (heartbeat). ID dari form, query,
// header X-Device-Id, atau default ESP32-GED-A1; token hanya diperiksa di
// production. Melakukan upsert devices (lihat risiko di docs/devices.md §7).
func (s *Server) devicePing(w http.ResponseWriter, r *http.Request) {
	deviceID := strings.TrimSpace(r.FormValue("device_id"))
	if deviceID == "" {
		deviceID = strings.TrimSpace(r.URL.Query().Get("device_id"))
	}
	if deviceID == "" {
		deviceID = strings.TrimSpace(r.Header.Get("X-Device-Id"))
	}
	if deviceID == "" {
		deviceID = "ESP32-GED-A1"
	}

	if s.config.Environment == "production" && !s.validDeviceToken(r) {
		writeError(w, http.StatusUnauthorized, "invalid_token", "Device token tidak valid.")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Collection("devices").UpdateOne(r.Context(),
		bson.M{"_id": deviceID},
		bson.M{
			"$set":         bson.M{"last_seen": now},
			"$setOnInsert": bson.M{"_id": deviceID, "name": "Perangkat " + deviceID},
		},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal memperbarui status perangkat.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"device_id": deviceID,
		"last_seen": now,
		"message":   "Perangkat berhasil terhubung dan status aktif.",
	})
}

// health: GET /api/v1/health (publik) — status face service dan penyimpanan
// foto. ?device_id= juga memperbarui last_seen perangkat (upsert, tanpa token).
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	if deviceID != "" {
		now := time.Now().UTC().Format(time.RFC3339)
		_, _ = s.db.Collection("devices").UpdateOne(r.Context(),
			bson.M{"_id": deviceID},
			bson.M{
				"$set":         bson.M{"last_seen": now},
				"$setOnInsert": bson.M{"_id": deviceID, "name": "Perangkat " + deviceID},
			},
			options.Update().SetUpsert(true),
		)
	}
	face := s.faceHealth(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "faceVerification": face, "imageStorage": s.imageStorageHealth(r.Context())})
}

// faceHealth membaca GET /health face service (services/face/api.py), yang
// mengembalikan {"status": "ok"|"initializing", "model_loaded": bool,
// "det_model": "...", "rec_model": "..."}.
func (s *Server) faceHealth(ctx context.Context) map[string]any {
	offline := map[string]any{"online": false, "provider": "Python face service"}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.config.FaceServiceURL, "/")+"/health", nil)
	if err != nil {
		return offline
	}
	if s.config.FaceServiceToken != "" {
		request.Header.Set("Authorization", "Bearer "+s.config.FaceServiceToken)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return offline
	}
	defer response.Body.Close()
	var data struct {
		Status      string `json:"status"`
		ModelLoaded bool   `json:"model_loaded"`
		DetModel    string `json:"det_model"`
		RecModel    string `json:"rec_model"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&data) != nil {
		return offline
	}
	result := map[string]any{
		"online":   data.ModelLoaded,
		"provider": "Python YOLO-FaceV2 + ArcFace",
	}
	if data.DetModel != "" {
		result["detectionModel"] = data.DetModel
	}
	if data.RecModel != "" {
		result["recognitionModel"] = data.RecModel
	}
	return result
}

func (s *Server) lastDeviceSeen(ctx context.Context) (*time.Time, error) {
	opts := options.FindOne().SetSort(bson.D{{Key: "last_seen", Value: -1}})
	var doc struct {
		LastSeen string `bson:"last_seen"`
	}
	err := s.db.Collection("devices").FindOne(ctx, bson.M{"last_seen": bson.M{"$exists": true, "$ne": ""}}, opts).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	parsed, err := time.Parse(time.RFC3339, doc.LastSeen)
	if err != nil {
		return nil, nil
	}
	return &parsed, nil
}

func principalFromRequest(r *http.Request) (principal, bool) {
	user, ok := security.PrincipalFromContext(r.Context())
	if !ok {
		return principal{}, false
	}
	return principal{role: user.Role, classes: user.ClassIDs}, true
}

func (s *Server) validDeviceToken(r *http.Request) bool {
	expected := s.config.DeviceToken
	return expected != "" && secureEqual(bearerToken(r), expected)
}

func bearerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < 7 || !strings.EqualFold(value[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(value[7:])
}

func secureEqual(actual, expected string) bool {
	if len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func formatTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Local().Format("15:04")
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func ParseConfig() Config {
	return Config{
		Environment:        env("APP_ENV", "development"),
		FaceServiceURL:     env("FACE_SERVICE_URL", "http://127.0.0.1:8000"),
		FaceServiceToken:   os.Getenv("FACE_SERVICE_TOKEN"),
		DeviceToken:        os.Getenv("DEVICE_TOKEN"),
		SimThreshold:       envFloat("FACE_SIM_THRESHOLD", 0.20),
		PoseThreshold:      envFloat("FACE_POSE_THRESHOLD", 0.20),
		ScanMinMatchFrames: int(envFloat("SCAN_MIN_MATCH_FRAMES", 2)),
		FaceServiceTimeout: time.Duration(envFloat("FACE_SERVICE_TIMEOUT_SECONDS", 15) * float64(time.Second)),
		ImageStorage: storage.Config{
			Endpoint:        strings.TrimSpace(os.Getenv("S3_ENDPOINT")),
			Region:          env("S3_REGION", "garage"),
			Bucket:          strings.TrimSpace(os.Getenv("S3_BUCKET")),
			AccessKeyID:     strings.TrimSpace(os.Getenv("S3_ACCESS_KEY_ID")),
			SecretAccessKey: strings.TrimSpace(os.Getenv("S3_SECRET_ACCESS_KEY")),
		},
		ImageRetentionDays:       int(envFloat("IMAGE_RETENTION_DAYS", 30)),
		ImageReviewRetentionDays: int(envFloat("IMAGE_REVIEW_RETENTION_DAYS", 90)),
	}
}

func envFloat(key string, fallback float64) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(key)), 64)
	if err != nil {
		return fallback
	}
	return value
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
