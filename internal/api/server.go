package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"dash.algorithmics/internal/security"
	"dash.algorithmics/internal/store"
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

func (s *Server) classOverview(ctx context.Context, user principal) ([]ClassRow, error) {
	query := `SELECT c.id, c.name,
		COUNT(DISTINCT cs.student_id),
		COUNT(DISTINCT CASE WHEN ft.student_id IS NOT NULL THEN cs.student_id END),
		COUNT(DISTINCT CASE WHEN a.status = 'Hadir' THEN a.student_id END)
		FROM classes c
		LEFT JOIN class_students cs ON cs.class_id = c.id
		LEFT JOIN face_templates ft ON ft.student_id = cs.student_id
		LEFT JOIN scan_events a ON a.class_id = c.id AND a.status = 'Hadir' AND date(a.scanned_at, 'localtime') = date('now', 'localtime')`
	args := []any{}
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return []ClassRow{}, nil
		}
		query += ` WHERE c.id IN (` + placeholders(len(user.classes)) + `)`
		for _, classID := range user.classes {
			args = append(args, classID)
		}
	}
	query += ` GROUP BY c.id, c.name ORDER BY c.id`
	rows, err := s.db.SQL.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ClassRow, 0, 3)
	for rows.Next() {
		var row ClassRow
		if err := rows.Scan(&row.ID, &row.Name, &row.StudentCount, &row.Enrolled, &row.Present); err != nil {
			return nil, err
		}
		if row.StudentCount > 0 {
			row.AttendanceRate = row.Present * 100 / row.StudentCount
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *Server) scanStatusCounts(ctx context.Context, user principal) (map[string]int, error) {
	query := `SELECT status, COUNT(*) FROM scan_events WHERE date(scanned_at, 'localtime') = date('now', 'localtime')`
	args := []any{}
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return map[string]int{}, nil
		}
		query += ` AND class_id IN (` + placeholders(len(user.classes)) + `)`
		for _, classID := range user.classes {
			args = append(args, classID)
		}
	}
	query += ` GROUP BY status`
	rows, err := s.db.SQL.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

func (s *Server) recentEvents(ctx context.Context, user principal) ([]ScanEvent, error) {
	query := `SELECT se.scanned_at, se.status, se.confidence, se.review_reason, se.device_id, st.nim, st.name, c.id, c.name
		FROM scan_events se
		LEFT JOIN students st ON st.id = se.student_id
		JOIN classes c ON c.id = se.class_id`
	args := []any{}
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return []ScanEvent{}, nil
		}
		query += ` WHERE se.class_id IN (` + placeholders(len(user.classes)) + `)`
		for _, classID := range user.classes {
			args = append(args, classID)
		}
	}
	query += ` ORDER BY se.scanned_at DESC, se.id DESC LIMIT 50`
	rows, err := s.db.SQL.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ScanEvent, 0, 50)
	for rows.Next() {
		var event ScanEvent
		var nim, name sql.NullString
		if err := rows.Scan(&event.ScannedAt, &event.Status, &event.Confidence, &event.ReviewReason, &event.DeviceID, &nim, &name, &event.ClassID, &event.ClassName); err != nil {
			return nil, err
		}
		event.StudentNIM = nim.String
		event.StudentName = name.String
		result = append(result, event)
	}
	return result, rows.Err()
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
// agar fragment HTML (htmx) dapat memakai ulang SQL yang sama tanpa duplikasi.
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
	id := "student-" + strings.ToLower(nim)
	if _, err := s.db.SQL.ExecContext(ctx, `INSERT INTO students(id, nim, name) VALUES(?, ?, ?)`, id, nim, name); err != nil {
		return "", ErrStudentExists
	}
	if _, err := s.db.SQL.ExecContext(ctx, `INSERT INTO class_students(class_id, student_id) VALUES(?, ?)`, classID, id); err != nil {
		_, _ = s.db.SQL.ExecContext(ctx, `DELETE FROM students WHERE id = ?`, id)
		return "", ErrInvalidClass
	}
	return id, nil
}

// EnrollFace mengirim gambar ke layanan verifikasi wajah untuk NIM tertentu.
func (s *Server) EnrollFace(ctx context.Context, nim string, image []byte, filename string) (map[string]any, error) {
	nim = strings.ToUpper(strings.TrimSpace(nim))
	var studentID string
	if err := s.db.SQL.QueryRowContext(ctx, `SELECT id FROM students WHERE nim = ?`, nim).Scan(&studentID); err != nil {
		return nil, ErrStudentNotFound
	}
	var response map[string]any
	if err := s.callFaceService(ctx, "/enroll", map[string]string{"student_id": studentID}, "image", filename, image, &response); err != nil {
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
	query := `SELECT c.id, c.name, COUNT(DISTINCT cs.student_id),
		COUNT(DISTINCT CASE WHEN a.status = 'Hadir' THEN a.student_id END)
		FROM classes c
		LEFT JOIN class_students cs ON cs.class_id = c.id
		LEFT JOIN scan_events a ON a.class_id = c.id AND a.status = 'Hadir' AND date(a.scanned_at, 'localtime') = date('now', 'localtime')`
	args := []any{}
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return []map[string]any{}, nil
		}
		query += ` WHERE c.id IN (` + placeholders(len(user.classes)) + `)`
		for _, classID := range user.classes {
			args = append(args, classID)
		}
	}
	query += ` GROUP BY c.id, c.name ORDER BY c.id`
	rows, err := s.db.SQL.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]map[string]any, 0, 3)
	for rows.Next() {
		var id, name string
		var total, present int
		if err := rows.Scan(&id, &name, &total, &present); err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"id": id, "name": name, "studentCount": total, "present": present})
	}
	return result, rows.Err()
}

func (s *Server) dashboardAttendance(ctx context.Context, user principal) ([]RosterEntry, error) {
	query := `SELECT st.id, st.nim, st.name, c.id, c.name, e.scanned_at, COALESCE(e.face_verified, 0), e.device_id, e.status,
		EXISTS(SELECT 1 FROM face_templates ft WHERE ft.student_id = st.id)
		FROM students st
		JOIN class_students cs ON cs.student_id = st.id
		JOIN classes c ON c.id = cs.class_id
		LEFT JOIN scan_events e ON e.id = (
			SELECT se.id FROM scan_events se WHERE se.student_id = st.id AND se.class_id = c.id
			AND date(se.scanned_at, 'localtime') = date('now', 'localtime') ORDER BY se.scanned_at DESC, se.id DESC LIMIT 1
		)`
	args := []any{}
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return []RosterEntry{}, nil
		}
		query += ` WHERE c.id IN (` + placeholders(len(user.classes)) + `)`
		for _, classID := range user.classes {
			args = append(args, classID)
		}
	}
	query += ` ORDER BY c.id, st.nim`
	rows, err := s.db.SQL.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]RosterEntry, 0, 5)
	for rows.Next() {
		var entry RosterEntry
		var scannedAt, deviceID, status sql.NullString
		if err := rows.Scan(&entry.StudentID, &entry.StudentNIM, &entry.StudentName, &entry.ClassID, &entry.ClassName, &scannedAt, &entry.FaceVerified, &deviceID, &status, &entry.FaceEnrolled); err != nil {
			return nil, err
		}
		entry.Status = "Belum hadir"
		if status.Valid {
			entry.Status = status.String
		}
		entry.DeviceID = deviceID.String
		if scannedAt.Valid {
			value := scannedAt.String
			entry.ScannedAt = &value
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func (s *Server) countStudents(ctx context.Context, user principal) (int, error) {
	query, args := scopedStudentCount(user)
	var count int
	err := s.db.SQL.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

func (s *Server) countTemplates(ctx context.Context, user principal) (int, error) {
	query := `SELECT COUNT(DISTINCT ft.student_id) FROM face_templates ft JOIN students st ON st.id = ft.student_id`
	args := []any{}
	if user.role == "dosen" {
		query += ` JOIN class_students cs ON cs.student_id = st.id WHERE cs.class_id IN (` + placeholders(len(user.classes)) + `)`
		for _, classID := range user.classes {
			args = append(args, classID)
		}
	}
	var count int
	err := s.db.SQL.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

func (s *Server) countScans(ctx context.Context, user principal) (int, int, error) {
	query := `SELECT COUNT(CASE WHEN status = 'Hadir' THEN 1 END), COUNT(CASE WHEN status = 'Perlu ditinjau' THEN 1 END) FROM scan_events WHERE date(scanned_at, 'localtime') = date('now', 'localtime')`
	args := []any{}
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return 0, 0, nil
		}
		query += ` AND class_id IN (` + placeholders(len(user.classes)) + `)`
		for _, classID := range user.classes {
			args = append(args, classID)
		}
	}
	var verified, review int
	err := s.db.SQL.QueryRowContext(ctx, query, args...).Scan(&verified, &review)
	return verified, review, err
}

func scopedStudentCount(user principal) (string, []any) {
	if user.role == "dosen" {
		if len(user.classes) == 0 {
			return `SELECT 0`, nil
		}
		return `SELECT COUNT(DISTINCT student_id) FROM class_students WHERE class_id IN (` + placeholders(len(user.classes)) + `)`, stringArgs(user.classes)
	}
	return `SELECT COUNT(*) FROM students`, nil
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
	var exists int
	if err = s.db.SQL.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM devices WHERE id = ?`, deviceID).Scan(&exists); err != nil || exists == 0 {
		writeError(w, http.StatusForbidden, "unknown_device", "Perangkat belum terdaftar.")
		return
	}
	if err = s.db.SQL.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM classes WHERE id = ?`, classID).Scan(&exists); err != nil || exists == 0 {
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
		var enrolled int
		if err = s.db.SQL.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM class_students WHERE class_id = ? AND student_id = ?`, classID, studentID).Scan(&enrolled); err != nil {
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
	_, err = s.db.SQL.ExecContext(r.Context(), `
		INSERT INTO scan_events(student_id, class_id, device_id, scanned_at, face_verified, liveness_passed, confidence, status, review_reason)
		VALUES(NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?)
	`, studentID, classID, deviceID, scannedAt.Format(time.RFC3339), faceVerified, strings.EqualFold(r.FormValue("liveness_passed"), "true"), verification.Confidence, status, reason)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal menyimpan event scan.")
		return
	}
	_, _ = s.db.SQL.ExecContext(r.Context(), `UPDATE devices SET last_seen = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), deviceID)
	var student map[string]any
	if faceVerified {
		var nim, name string
		if err = s.db.SQL.QueryRowContext(r.Context(), `SELECT nim, name FROM students WHERE id = ?`, studentID).Scan(&nim, &name); err == nil {
			student = map[string]any{"id": studentID, "nim": nim, "name": name}
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": status, "student": student, "faceVerified": faceVerified,
		"confidence": verification.Confidence, "reviewReason": reason,
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
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
	var value sql.NullString
	err := s.db.SQL.QueryRowContext(ctx, `SELECT MAX(last_seen) FROM devices`).Scan(&value)
	if err != nil || !value.Valid {
		return nil, err
	}
	parsed, err := time.Parse(time.RFC3339, value.String)
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
