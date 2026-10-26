package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"dash.algorithmics/internal/store"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Fitur administrator: hapus mahasiswa dan kelola akun dosen. Dipakai bersama
// endpoint JSON (/api/v1/...) dan fragment htmx (/partials/...).

var (
	ErrInvalidUsername  = errors.New("invalid username")
	ErrPasswordTooShort = errors.New("password too short")
	ErrUsernameTaken    = errors.New("username already exists")
	ErrNoClasses        = errors.New("at least one class required")
	ErrUserNotFound     = errors.New("user not found")
	ErrNotLecturer      = errors.New("only lecturer accounts can be deleted")
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9._-]{3,32}$`)

// DeleteStudent menghapus mahasiswa beserta seluruh data biometrik dan
// riwayatnya: template wajah, foto enrollment & foto scan di Garage, event
// scan, dan relasi kelas. Mengembalikan nama mahasiswa yang dihapus.
func (s *Server) DeleteStudent(ctx context.Context, nim string) (string, error) {
	nim = strings.ToUpper(strings.TrimSpace(nim))
	var student struct {
		ID   string `bson:"_id"`
		Name string `bson:"name"`
	}
	if err := s.db.Collection("students").FindOne(ctx, bson.M{"nim": nim}).Decode(&student); err != nil {
		return "", ErrStudentNotFound
	}

	// Kumpulkan kunci foto lebih dulu, lalu hapus fotonya dari Garage.
	var keys []string
	var template struct {
		ImageKey string `bson:"image_key"`
	}
	if s.db.Collection("face_templates").FindOne(ctx, bson.M{"student_id": student.ID},
		options.FindOne().SetProjection(bson.M{"image_key": 1})).Decode(&template) == nil && template.ImageKey != "" {
		keys = append(keys, template.ImageKey)
	}
	cursor, err := s.db.Collection("scan_events").Find(ctx,
		bson.M{"student_id": student.ID, "image_key": bson.M{"$exists": true, "$ne": ""}},
		options.Find().SetProjection(bson.M{"image_key": 1}))
	if err != nil {
		return "", err
	}
	var events []struct {
		ImageKey string `bson:"image_key"`
	}
	if err := cursor.All(ctx, &events); err != nil {
		return "", err
	}
	for _, event := range events {
		keys = append(keys, event.ImageKey)
	}
	for _, key := range keys {
		s.deleteImage(ctx, key)
	}

	for _, step := range []struct {
		collection string
		filter     bson.M
	}{
		{"scan_events", bson.M{"student_id": student.ID}},
		{"face_templates", bson.M{"student_id": student.ID}},
		{"class_students", bson.M{"student_id": student.ID}},
		{"students", bson.M{"_id": student.ID}},
	} {
		if _, err := s.db.Collection(step.collection).DeleteMany(ctx, step.filter); err != nil {
			return "", err
		}
	}
	log.Printf("mahasiswa %s (%s) dihapus beserta %d foto", nim, student.ID, len(keys))
	return student.Name, nil
}

// StaffAccounts mengembalikan semua akun staf (tanpa hash password).
func (s *Server) StaffAccounts(ctx context.Context) ([]store.StaffSummary, error) {
	return s.db.ListStaff(ctx)
}

// LecturerInput adalah data pembuatan akun dosen.
type LecturerInput struct {
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Password    string   `json:"password"`
	ClassIDs    []string `json:"classIds"`
}

// CreateLecturer memvalidasi lalu membuat akun dosen. Username disimpan huruf
// kecil; kelas harus ada di koleksi classes.
func (s *Server) CreateLecturer(ctx context.Context, input LecturerInput) error {
	username := strings.ToLower(strings.TrimSpace(input.Username))
	displayName := strings.TrimSpace(input.DisplayName)
	if username == "" || displayName == "" || input.Password == "" {
		return ErrMissingFields
	}
	if !usernamePattern.MatchString(username) {
		return ErrInvalidUsername
	}
	if len(input.Password) < store.MinPasswordLength {
		return ErrPasswordTooShort
	}
	classes := make([]string, 0, len(input.ClassIDs))
	seen := map[string]bool{}
	for _, id := range input.ClassIDs {
		id = strings.ToUpper(strings.TrimSpace(id))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		classes = append(classes, id)
	}
	if len(classes) == 0 {
		return ErrNoClasses
	}
	count, err := s.db.Collection("classes").CountDocuments(ctx, bson.M{"_id": bson.M{"$in": classes}})
	if err != nil {
		return err
	}
	if int(count) != len(classes) {
		return ErrInvalidClass
	}
	taken, err := s.db.UsernameTaken(ctx, username)
	if err != nil {
		return err
	}
	if taken {
		return ErrUsernameTaken
	}
	if err := s.db.CreateUser(ctx, username, input.Password, displayName, "dosen", classes); err != nil {
		if errors.Is(err, store.ErrUserExists) {
			return ErrUsernameTaken
		}
		return err
	}
	log.Printf("akun dosen %q dibuat untuk kelas %v", username, classes)
	return nil
}

// DeleteLecturer menghapus akun dosen. Akun administrator tidak dapat dihapus
// lewat fitur ini agar administrator tidak mengunci dirinya sendiri.
func (s *Server) DeleteLecturer(ctx context.Context, id string) error {
	role, err := s.db.UserRole(ctx, strings.TrimSpace(id))
	if err != nil {
		return ErrUserNotFound
	}
	if role != "dosen" {
		return ErrNotLecturer
	}
	deleted, err := s.db.DeleteUser(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrUserNotFound
	}
	return nil
}

// LecturerErrorMessage memetakan error pembuatan/penghapusan akun ke pesan UI.
func LecturerErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrMissingFields):
		return "Username, nama, dan password wajib diisi."
	case errors.Is(err, ErrInvalidUsername):
		return "Username 3–32 karakter: huruf kecil, angka, titik, garis bawah, atau tanda hubung."
	case errors.Is(err, ErrPasswordTooShort):
		return "Password minimal " + strconv.Itoa(store.MinPasswordLength) + " karakter."
	case errors.Is(err, ErrNoClasses):
		return "Pilih minimal satu kelas yang diampu."
	case errors.Is(err, ErrInvalidClass):
		return "Kelas tidak ditemukan."
	case errors.Is(err, ErrUsernameTaken):
		return "Username sudah dipakai."
	case errors.Is(err, ErrUserNotFound):
		return "Akun tidak ditemukan."
	case errors.Is(err, ErrNotLecturer):
		return "Hanya akun dosen yang dapat dihapus dari halaman ini."
	default:
		return "Aksi gagal."
	}
}

// ----- Endpoint JSON (administrator + CSRF) -----

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	user, ok := principalFromRequest(r)
	if !ok || user.role != "administrator" {
		writeError(w, http.StatusForbidden, "forbidden", "Hanya administrator yang dapat melakukan aksi ini.")
		return false
	}
	return true
}

func (s *Server) deleteStudent(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	nim := strings.ToUpper(strings.TrimSpace(r.PathValue("nim")))
	name, err := s.DeleteStudent(r.Context(), nim)
	switch {
	case errors.Is(err, ErrStudentNotFound):
		writeError(w, http.StatusNotFound, "student_not_found", "Mahasiswa tidak ditemukan.")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal menghapus mahasiswa.")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"nim": nim, "name": name, "deleted": true})
	}
}

func (s *Server) listStaff(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	accounts, err := s.StaffAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal memuat akun staf.")
		return
	}
	items := make([]map[string]any, 0, len(accounts))
	for _, a := range accounts {
		items = append(items, map[string]any{
			"id": a.ID, "username": a.Username, "displayName": a.DisplayName, "role": a.Role,
			"active": a.Active, "classIds": a.ClassIDs, "createdAt": a.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"staff": items})
}

func (s *Server) createLecturer(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var input LecturerInput
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Body JSON tidak valid.")
		return
	}
	if err := s.CreateLecturer(r.Context(), input); err != nil {
		status, code := http.StatusBadRequest, "invalid_input"
		switch {
		case errors.Is(err, ErrUsernameTaken):
			status, code = http.StatusConflict, "username_taken"
		case errors.Is(err, ErrMissingFields), errors.Is(err, ErrInvalidUsername), errors.Is(err, ErrPasswordTooShort),
			errors.Is(err, ErrNoClasses), errors.Is(err, ErrInvalidClass):
		default:
			status, code = http.StatusInternalServerError, "database_error"
		}
		writeError(w, status, code, LecturerErrorMessage(err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"username": strings.ToLower(strings.TrimSpace(input.Username)), "role": "dosen"})
}

func (s *Server) deleteStaff(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	err := s.DeleteLecturer(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, ErrUserNotFound):
		writeError(w, http.StatusNotFound, "user_not_found", LecturerErrorMessage(err))
	case errors.Is(err, ErrNotLecturer):
		writeError(w, http.StatusForbidden, "not_lecturer", LecturerErrorMessage(err))
	case err != nil:
		writeError(w, http.StatusInternalServerError, "database_error", "Gagal menghapus akun.")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "deleted": true})
	}
}
