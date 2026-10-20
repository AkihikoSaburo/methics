package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"dash.algorithmics/internal/security"
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
}

type Server struct {
	db     *store.Database
	config Config
	client *http.Client
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
}

func New(db *store.Database, config Config) *Server {
	return &Server{
		db:     db,
		config: config,
		client: &http.Client{Timeout: 8 * time.Second},
	}
}

func (s *Server) StaffHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/dashboard", s.dashboard)
	mux.HandleFunc("GET /api/v1/classes", s.classes)
	mux.HandleFunc("GET /api/v1/reports", s.reports)
	mux.HandleFunc("POST /api/v1/students", s.createStudent)
	mux.HandleFunc("POST /api/v1/students/{nim}/face-template", s.enrollFace)
	return securityHeaders(mux)
}

func (s *Server) DeviceHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/devices/ping", s.devicePing)
	mux.HandleFunc("POST /api/v1/devices/ping", s.devicePing)
	mux.HandleFunc("POST /api/v1/scans", s.receiveScan)
	return securityHeaders(mux)
}

func (s *Server) StaticHandler(staticDir string) http.Handler {
	return securityHeaders(http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
}

func (s *Server) Handler(staticDir string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/scans", s.DeviceHandler())
	mux.Handle("/api/v1/health", s.DeviceHandler())
	mux.Handle("/api/v1/devices/", s.DeviceHandler())
	mux.Handle("/api/v1/", s.StaffHandler())
	mux.Handle("/static/", s.StaticHandler(staticDir))
	return mux
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

// AllScanEvents mengembalikan seluruh event scan (tanpa batas tanggal) dengan
// filter opsional status dan kelas. Dibatasi 200 event terbaru.
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

// DeviceStatus melaporkan apakah perangkat ESP32 baru-baru ini mengirim data.
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
		})
	}
	return result
}

// RosterEntry adalah satu baris roster mahasiswa: identitas, kelas, dan status
// scan terakhir hari ini. Dipakai endpoint JSON dashboard dan fragment /mahasiswa.
type RosterEntry struct {
	StudentID    string
	StudentNIM   string
	StudentName  string
	ClassID      string
	ClassName    string
	ScannedAt    *string
	FaceVerified bool
	FaceEnrolled bool
	DeviceID     string
	Status       string
}

var (
	ErrMissingFields   = errors.New("missing fields")
	ErrStudentExists   = errors.New("student exists")
	ErrInvalidClass    = errors.New("invalid class")
	ErrStudentNotFound = errors.New("student not found")
	ErrFaceService     = errors.New("face service error")
)

// Roster mengembalikan roster mahasiswa ber-scope role untuk fragment htmx.
func (s *Server) Roster(ctx context.Context, role string, classes []string) ([]RosterEntry, error) {
	return s.dashboardAttendance(ctx, principal{role: role, classes: classes})
}

// CreateStudent memvalidasi dan menyimpan mahasiswa baru. Dipakai endpoint JSON
// dan fragment htmx agar aturan validasi tetap satu sumber.
func (s *Server) CreateStudent(ctx context.Context, name, nim, classID string) (string, error) {
	name = strings.TrimSpace(name)
	nim = strings.TrimSpace(strings.ToUpper(nim))
	classID = strings.TrimSpace(strings.ToUpper(classID))
	if name == "" || nim == "" || classID == "" {
		return "", ErrMissingFields
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

// EnrollFace mengirim gambar ke layanan verifikasi wajah untuk NIM tertentu.
func (s *Server) EnrollFace(ctx context.Context, nim string, image []byte, filename string) (map[string]any, error) {
	nim = strings.ToUpper(strings.TrimSpace(nim))
	var student struct {
		ID string `bson:"_id"`
	}
	err := s.db.Collection("students").FindOne(ctx, bson.M{"nim": nim}).Decode(&student)
	if err != nil {
		return nil, ErrStudentNotFound
	}
	var response map[string]any
	if err := s.callFaceService(ctx, "/enroll", map[string]string{"student_id": student.ID}, "image", filename, image, &response); err != nil {
		return nil, ErrFaceService
	}
	return response, nil
}

// ReadImagePart membaca file gambar (maks 8 MB) dari field multipart "image",
// dipakai fragment htmx untuk enrollment wajah.
func ReadImagePart(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	return readImagePart(w, r)
}

func rosterToMaps(entries []RosterEntry) []map[string]any {
	result := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		var scanTime any
		if entry.ScannedAt != nil {
			scanTime = *entry.ScannedAt
		}
		result = append(result, map[string]any{
			"student":      map[string]any{"id": entry.StudentID, "nim": entry.StudentNIM, "name": entry.StudentName},
			"class":        map[string]any{"id": entry.ClassID, "name": entry.ClassName},
			"scannedAt":    scanTime,
			"faceVerified": entry.FaceVerified,
			"faceEnrolled": entry.FaceEnrolled,
			"deviceId":     entry.DeviceID,
			"status":       entry.Status,
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

	enrolledMap := make(map[string]bool)
	ftCur, err := s.db.Collection("face_templates").Find(ctx, bson.M{"student_id": bson.M{"$in": studentIDs}})
	if err == nil {
		var ftList []struct {
			StudentID string `bson:"student_id"`
		}
		if err := ftCur.All(ctx, &ftList); err == nil {
			for _, ft := range ftList {
				enrolledMap[ft.StudentID] = true
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
			StudentID:    st.ID,
			StudentNIM:   st.NIM,
			StudentName:  st.Name,
			ClassID:      st.ClassID,
			ClassName:    classesMap[st.ClassID],
			Status:       "Belum hadir",
			FaceEnrolled: enrolledMap[st.ID],
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
	image, filename, err := readImagePart(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_image", err.Error())
		return
	}
	response, err := s.EnrollFace(r.Context(), nim, image, filename)
	if err != nil {
		switch {
		case errors.Is(err, ErrStudentNotFound):
			writeError(w, http.StatusNotFound, "student_not_found", "Mahasiswa tidak ditemukan.")
		case errors.Is(err, ErrFaceService):
			writeError(w, http.StatusBadGateway, "face_service_error", "Enrollment gagal; pastikan service Python dan model wajah aktif.")
		default:
			writeError(w, http.StatusInternalServerError, "enroll_error", "Enrollment gagal.")
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"nim": nim, "faceEnrolled": true, "result": response})
}

func (s *Server) receiveScan(w http.ResponseWriter, r *http.Request) {
	if !s.validDeviceToken(r) {
		writeError(w, http.StatusUnauthorized, "invalid_device", "Token perangkat tidak valid.")
		return
	}
	image, filename, err := readImagePart(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_image", err.Error())
		return
	}
	deviceID := strings.TrimSpace(r.FormValue("device_id"))
	classID := strings.TrimSpace(strings.ToUpper(r.FormValue("class_id")))
	capturedAtRaw := strings.TrimSpace(r.FormValue("captured_at"))
	livenessRaw := strings.ToLower(strings.TrimSpace(r.FormValue("liveness_passed")))
	if deviceID == "" || classID == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "device_id dan class_id wajib diisi.")
		return
	}
	capturedAt, parseErr := time.Parse(time.RFC3339, capturedAtRaw)
	if parseErr != nil || time.Since(capturedAt) > 5*time.Minute || time.Until(capturedAt) > 5*time.Minute {
		writeError(w, http.StatusBadRequest, "stale_capture", "captured_at wajib RFC3339 dan berjarak maksimal 5 menit dari waktu server.")
		return
	}
	if livenessRaw != "true" && livenessRaw != "false" {
		writeError(w, http.StatusBadRequest, "invalid_liveness", "liveness_passed wajib bernilai true atau false.")
		return
	}
	if !s.validScanSignature(r, deviceID, classID, capturedAtRaw, livenessRaw, image) {
		writeError(w, http.StatusUnauthorized, "invalid_signature", "Signature scan tidak valid atau timestamp kedaluwarsa.")
		return
	}
	deviceCount, err := s.db.Collection("devices").CountDocuments(r.Context(), bson.M{"_id": deviceID})
	if err != nil || deviceCount == 0 {
		writeError(w, http.StatusForbidden, "unknown_device", "Perangkat belum terdaftar.")
		return
	}
	classCount, err := s.db.Collection("classes").CountDocuments(r.Context(), bson.M{"_id": classID})
	if err != nil || classCount == 0 {
		writeError(w, http.StatusBadRequest, "unknown_class", "Kelas tidak ditemukan.")
		return
	}
	scannedAt := capturedAt.UTC()
	var verification struct {
		Verified   bool    `json:"verified"`
		StudentID  string  `json:"studentId"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	}
	if err = s.callFaceService(r.Context(), "/verify", map[string]string{"class_id": classID}, "image", filename, image, &verification); err != nil {
		writeError(w, http.StatusBadGateway, "face_service_error", "Verifikasi tidak tersedia; scan tidak dicatat sebagai hadir.")
		return
	}
	studentID := verification.StudentID
	faceVerified := verification.Verified && studentID != ""
	status := "Ditolak"
	reason := verification.Reason
	if faceVerified {
		enrolled, err := s.db.Collection("students").CountDocuments(r.Context(), bson.M{"_id": studentID, "class_id": classID})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "database_error", "Gagal memvalidasi kelas mahasiswa.")
			return
		}
		if enrolled == 0 {
			status = "Perlu ditinjau"
			reason = "Mahasiswa terverifikasi tetapi bukan anggota kelas pada perangkat."
		} else if !strings.EqualFold(r.FormValue("liveness_passed"), "true") {
			status = "Perlu ditinjau"
			reason = "Cocok wajah; bukti liveness belum tersedia atau tidak lolos."
		} else {
			status = "Hadir"
			reason = ""
		}
	}
	_, err = s.db.Collection("scan_events").InsertOne(r.Context(), bson.M{
		"student_id":      studentID,
		"class_id":        classID,
		"device_id":       deviceID,
		"scanned_at":      scannedAt.Format(time.RFC3339),
		"face_verified":   faceVerified,
		"liveness_passed": strings.EqualFold(r.FormValue("liveness_passed"), "true"),
		"confidence":      verification.Confidence,
		"status":          status,
		"review_reason":   reason,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal menyimpan event scan.")
		return
	}
	_, _ = s.db.Collection("devices").UpdateOne(r.Context(),
		bson.M{"_id": deviceID},
		bson.M{"$set": bson.M{"last_seen": time.Now().UTC().Format(time.RFC3339)}},
	)
	var student map[string]any
	if faceVerified {
		var st struct {
			NIM  string `bson:"nim"`
			Name string `bson:"name"`
		}
		if err = s.db.Collection("students").FindOne(r.Context(), bson.M{"_id": studentID}).Decode(&st); err == nil {
			student = map[string]any{"id": studentID, "nim": st.NIM, "name": st.Name}
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": status, "student": student, "faceVerified": faceVerified,
		"confidence": verification.Confidence, "reviewReason": reason,
	})
}

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
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "faceVerification": face})
}

func (s *Server) faceHealth(ctx context.Context) map[string]any {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.config.FaceServiceURL, "/")+"/health", nil)
	if err != nil {
		return map[string]any{"online": false, "provider": "Python service"}
	}
	if s.config.FaceServiceToken != "" {
		request.Header.Set("Authorization", "Bearer "+s.config.FaceServiceToken)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return map[string]any{"online": false, "provider": "Python service"}
	}
	defer response.Body.Close()
	var data struct {
		ModelReady bool `json:"modelReady"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&data) != nil {
		return map[string]any{"online": false, "provider": "Python service"}
	}
	return map[string]any{"online": data.ModelReady, "provider": "Python face_recognition"}
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

func (s *Server) validScanSignature(r *http.Request, deviceID, classID, capturedAt, liveness string, image []byte) bool {
	secret := s.config.DeviceToken
	timestamp := strings.TrimSpace(r.Header.Get("X-Device-Timestamp"))
	requestTime, err := time.Parse(time.RFC3339, timestamp)
	if err != nil || time.Since(requestTime) > 2*time.Minute || time.Until(requestTime) > 2*time.Minute {
		return false
	}
	imageHash := sha256.Sum256(image)
	message := strings.Join([]string{timestamp, deviceID, classID, capturedAt, liveness, hex.EncodeToString(imageHash[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	expected := hex.EncodeToString(mac.Sum(nil))
	provided := strings.TrimSpace(r.Header.Get("X-Device-Signature"))
	if len(provided) != len(expected) {
		return false
	}
	return hmac.Equal([]byte(provided), []byte(expected))
}

func (s *Server) callFaceService(ctx context.Context, endpoint string, fields map[string]string, fileField, filename string, image []byte, output any) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return err
		}
	}
	part, err := writer.CreateFormFile(fileField, filename)
	if err != nil {
		return err
	}
	if _, err = part.Write(image); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.config.FaceServiceURL, "/")+endpoint, &body)
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
		message, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("face service returned %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	return json.NewDecoder(response.Body).Decode(output)
}

func readImagePart(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return nil, "", errors.New("kirim multipart/form-data dengan file image maksimal 8 MB")
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		return nil, "", errors.New("field image wajib diisi")
	}
	defer file.Close()
	image, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
	if err != nil || len(image) == 0 || len(image) > 8<<20 {
		return nil, "", errors.New("file gambar kosong atau melebihi batas 8 MB")
	}
	return image, header.Filename, nil
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

func placeholders(count int) string {
	items := make([]string, count)
	for index := range items {
		items[index] = "?"
	}
	return strings.Join(items, ",")
}

func stringArgs(values []string) []any {
	args := make([]any, 0, len(values))
	for _, value := range values {
		args = append(args, value)
	}
	return args
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

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func ParseConfig() Config {
	return Config{
		Environment:      env("APP_ENV", "development"),
		FaceServiceURL:   env("FACE_SERVICE_URL", "http://127.0.0.1:8001"),
		FaceServiceToken: os.Getenv("FACE_SERVICE_TOKEN"),
		DeviceToken:      os.Getenv("DEVICE_TOKEN"),
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
