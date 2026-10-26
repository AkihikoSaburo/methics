package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"dash.algorithmics/internal/api"
	"dash.algorithmics/internal/security"
)

// Partials merender fragment HTML (htmx) untuk pembaruan sebagian halaman.
// Datanya diambil dari query/logika ber-scope role di api.Server agar tidak ada
// duplikasi query maupun aturan validasi antara endpoint JSON dan fragment.
type Partials struct {
	server    *api.Server
	templates *template.Template
}

func NewPartials(server *api.Server, viewDir string) (*Partials, error) {
	files, err := filepath.Glob(filepath.Join(viewDir, "fragments", "*.html"))
	if err != nil {
		return nil, err
	}
	funcs := template.FuncMap{
		"initials":    initials,
		"statusClass": statusClass,
		"confidence":  formatConfidence,
	}
	templates, err := template.New("fragments").Funcs(funcs).ParseFiles(files...)
	if err != nil {
		return nil, err
	}
	return &Partials{server: server, templates: templates}, nil
}

// Handler mendaftarkan seluruh endpoint fragment. Wajib dipasang di belakang
// middleware session karena fragment memakai principal dari context.
func (p *Partials) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /partials/dashboard", p.dashboard)
	mux.HandleFunc("GET /partials/kelas", p.kelas)
	mux.HandleFunc("GET /partials/laporan", p.laporan)
	mux.HandleFunc("GET /partials/mahasiswa", p.mahasiswa)
	mux.HandleFunc("GET /partials/mahasiswa/form", p.mahasiswaForm)
	mux.HandleFunc("POST /partials/mahasiswa/students", p.createStudent)
	mux.HandleFunc("POST /partials/mahasiswa/{nim}/face-template", p.enrollFace)
	mux.HandleFunc("DELETE /partials/mahasiswa/{nim}", p.deleteStudent)
	mux.HandleFunc("GET /partials/akses/staff", p.staff)
	mux.HandleFunc("POST /partials/akses/lecturers", p.createLecturer)
	mux.HandleFunc("DELETE /partials/akses/staff/{id}", p.deleteStaff)
	mux.HandleFunc("GET /partials/logs", p.logs)
	return mux
}

func principalOrError(w http.ResponseWriter, r *http.Request) (security.Principal, bool) {
	principal, ok := security.PrincipalFromContext(r.Context())
	if !ok {
		http.Error(w, "Autentikasi pengguna diperlukan.", http.StatusUnauthorized)
		return security.Principal{}, false
	}
	return principal, true
}

func (p *Partials) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := p.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "Gagal merender data.", http.StatusInternalServerError)
	}
}

type kelasFragmentData struct {
	Summary struct {
		Classes  int
		Students int
		Enrolled int
		Present  int
	}
	Classes []api.ClassRow
}

func (p *Partials) kelas(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalOrError(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	rows, err := p.server.ClassRows(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal memuat data kelas.", http.StatusInternalServerError)
		return
	}
	students, err := p.server.StudentCount(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal menghitung mahasiswa.", http.StatusInternalServerError)
		return
	}
	enrolled, err := p.server.TemplateCount(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal menghitung profil wajah.", http.StatusInternalServerError)
		return
	}

	data := kelasFragmentData{Classes: rows}
	data.Summary.Classes = len(rows)
	data.Summary.Students = students
	data.Summary.Enrolled = enrolled
	for _, row := range rows {
		data.Summary.Present += row.Present
	}
	p.render(w, "kelas_fragment", data)
}

type laporanBreakdownRow struct {
	Status  string
	Count   int
	Percent int
}

type laporanFragmentData struct {
	Summary struct {
		Verified       int
		Review         int
		Rejected       int
		AttendanceRate int
	}
	Breakdown []laporanBreakdownRow
	Classes   []api.ClassRow
	Events    []api.ScanEvent
}

func (p *Partials) laporan(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalOrError(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	overview, err := p.server.ClassRows(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal memuat data kelas.", http.StatusInternalServerError)
		return
	}
	statuses, err := p.server.ScanStatusCounts(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal menghitung status scan.", http.StatusInternalServerError)
		return
	}
	events, err := p.server.RecentEvents(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal memuat riwayat scan.", http.StatusInternalServerError)
		return
	}
	students, err := p.server.StudentCount(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal menghitung mahasiswa.", http.StatusInternalServerError)
		return
	}

	present := 0
	for _, row := range overview {
		present += row.Present
	}
	rate := 0
	if students > 0 {
		rate = present * 100 / students
	}

	data := laporanFragmentData{Classes: overview, Events: events}
	data.Summary.Verified = statuses["Hadir"]
	data.Summary.Review = statuses["Perlu ditinjau"]
	data.Summary.Rejected = statuses["Ditolak"]
	data.Summary.AttendanceRate = rate

	total := statuses["Hadir"] + statuses["Perlu ditinjau"] + statuses["Ditolak"]
	for _, status := range []string{"Hadir", "Perlu ditinjau", "Ditolak"} {
		count := statuses[status]
		if count == 0 {
			continue
		}
		percent := 0
		if total > 0 {
			percent = int(float64(count)/float64(total)*100 + 0.5)
		}
		data.Breakdown = append(data.Breakdown, laporanBreakdownRow{Status: status, Count: count, Percent: percent})
	}
	p.render(w, "laporan_fragment", data)
}

type mahasiswaRow struct {
	StudentNIM   string
	StudentName  string
	ClassID      string
	ClassName    string
	FaceEnrolled bool
	FaceImageURL string
	Status       string
}

type mahasiswaFragmentData struct {
	Role       string
	Total      int
	Enrolled   int
	ClassCount int
	Rows       []mahasiswaRow
	EmptyText  string
	Notice     string
	Error      string
}

func (p *Partials) mahasiswaRoster(ctx context.Context, principal security.Principal, query, classFilter, notice, errMessage string) (mahasiswaFragmentData, error) {
	entries, err := p.server.Roster(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		return mahasiswaFragmentData{}, err
	}
	classes, err := p.server.ClassRows(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		return mahasiswaFragmentData{}, err
	}

	data := mahasiswaFragmentData{Role: principal.Role, ClassCount: len(classes), Notice: notice, Error: errMessage}
	data.Total = len(entries)
	for _, entry := range entries {
		if entry.FaceEnrolled {
			data.Enrolled++
		}
	}

	needle := strings.ToLower(strings.TrimSpace(query))
	filtering := needle != "" || (classFilter != "" && classFilter != "all")
	for _, entry := range entries {
		if classFilter != "" && classFilter != "all" && entry.ClassID != classFilter {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(entry.StudentName+" "+entry.StudentNIM), needle) {
			continue
		}
		data.Rows = append(data.Rows, mahasiswaRow{
			StudentNIM: entry.StudentNIM, StudentName: entry.StudentName,
			ClassID: entry.ClassID, ClassName: entry.ClassName,
			FaceEnrolled: entry.FaceEnrolled, FaceImageURL: entry.FaceImageURL, Status: entry.Status,
		})
	}
	if len(data.Rows) == 0 {
		if filtering {
			data.EmptyText = "Tidak ada mahasiswa yang cocok."
		} else {
			data.EmptyText = "Belum ada mahasiswa dalam cakupan."
		}
	}
	return data, nil
}

func (p *Partials) mahasiswa(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalOrError(w, r)
	if !ok {
		return
	}
	data, err := p.mahasiswaRoster(r.Context(), principal, r.URL.Query().Get("q"), r.URL.Query().Get("class"), "", "")
	if err != nil {
		http.Error(w, "Gagal memuat roster mahasiswa.", http.StatusInternalServerError)
		return
	}
	p.render(w, "mahasiswa_fragment", data)
}

type studentFormData struct {
	Name    string
	NIM     string
	ClassID string
	Classes []api.ClassRow
	Error   string
}

func (p *Partials) studentForm(w http.ResponseWriter, r *http.Request, principal security.Principal, data studentFormData) {
	classes, err := p.server.ClassRows(r.Context(), principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal memuat data kelas.", http.StatusInternalServerError)
		return
	}
	data.Classes = classes
	p.render(w, "student_form", data)
}

func (p *Partials) mahasiswaForm(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalOrError(w, r)
	if !ok {
		return
	}
	if principal.Role != "administrator" {
		http.Error(w, "Hanya administrator yang dapat mendaftarkan mahasiswa.", http.StatusForbidden)
		return
	}
	p.studentForm(w, r, principal, studentFormData{})
}

func (p *Partials) createStudent(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalOrError(w, r)
	if !ok {
		return
	}
	if principal.Role != "administrator" {
		http.Error(w, "Hanya administrator yang dapat mendaftarkan mahasiswa.", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulir tidak valid.", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	nim := strings.TrimSpace(r.PostFormValue("nim"))
	classID := strings.TrimSpace(r.PostFormValue("classId"))

	if _, err := p.server.CreateStudent(r.Context(), name, nim, classID); err != nil {
		message := "Mahasiswa gagal disimpan."
		switch {
		case errors.Is(err, api.ErrMissingFields):
			message = "Nama, NIM, dan kelas wajib diisi."
		case errors.Is(err, api.ErrInvalidNIM):
			message = "NIM harus 9 digit angka, mis. 241011001."
		case errors.Is(err, api.ErrStudentExists):
			message = "NIM sudah terdaftar."
		case errors.Is(err, api.ErrInvalidClass):
			message = "Kelas tidak ditemukan."
		}
		p.studentForm(w, r, principal, studentFormData{Name: name, NIM: nim, ClassID: classID, Error: message})
		return
	}

	trigger, _ := json.Marshal(map[string]any{
		"toast":                map[string]any{"message": fmt.Sprintf("Mahasiswa %s berhasil ditambahkan.", name)},
		"student-dialog-close": true,
		"roster-changed":       true,
	})
	w.Header().Set("HX-Trigger", string(trigger))
	p.studentForm(w, r, principal, studentFormData{})
}

func (p *Partials) enrollFace(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalOrError(w, r)
	if !ok {
		return
	}
	if principal.Role != "administrator" {
		http.Error(w, "Hanya administrator yang dapat memulai enrollment wajah.", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	nim := strings.ToUpper(strings.TrimSpace(r.PathValue("nim")))

	var notice, errMessage string
	image, filename, err := api.ReadImagePart(w, r)
	switch {
	case err != nil:
		errMessage = err.Error()
	default:
		if result, err := p.server.EnrollFace(ctx, nim, image, filename); err != nil {
			switch {
			case errors.Is(err, api.ErrStudentNotFound):
				errMessage = "Mahasiswa tidak ditemukan."
			case errors.Is(err, api.ErrInvalidImage):
				errMessage = "Gambar tidak dapat dibaca; unggah foto JPEG/PNG yang valid."
			case errors.Is(err, api.ErrNoFace):
				errMessage = "Tidak ada wajah yang terdeteksi pada foto. Gunakan foto wajah yang jelas dan menghadap kamera."
			case errors.Is(err, api.ErrMultipleFaces):
				errMessage = "Foto berisi lebih dari satu wajah. Gunakan foto yang hanya berisi wajah mahasiswa tersebut."
			case errors.Is(err, api.ErrFaceAlreadyEnrolled):
				conflict, _ := result["conflictNim"].(string)
				errMessage = fmt.Sprintf("Wajah ini sudah terdaftar atas NIM %s; enrollment dibatalkan.", conflict)
			case errors.Is(err, api.ErrFaceService):
				errMessage = "Enrollment gagal; pastikan service Python dan model wajah aktif."
			default:
				errMessage = "Enrollment gagal."
			}
		} else {
			notice = fmt.Sprintf("Template wajah untuk %s berhasil diproses.", nim)
		}
	}

	data, err := p.mahasiswaRoster(ctx, principal, r.FormValue("q"), r.FormValue("class"), notice, errMessage)
	if err != nil {
		http.Error(w, "Gagal memuat roster mahasiswa.", http.StatusInternalServerError)
		return
	}
	p.render(w, "mahasiswa_fragment", data)
}

type dashboardScanRow struct {
	StudentName  string
	StudentNIM   string
	ClassID      string
	ScannedAt    string
	Verification string
	Status       string
	DeviceID     string
}

type dashboardFragmentData struct {
	Students       int
	Classes        int
	ClassCodes     string
	Verified       int
	Review         int
	FaceTemplates  int
	ClassRows      []api.ClassRow
	PresentTotal   int
	StudentTotal   int
	DeviceOnline   bool
	DeviceLastSeen string
	FaceOnline     bool
	FaceProvider   string
	Rows           []dashboardScanRow
}

func (p *Partials) dashboard(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalOrError(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	classes, err := p.server.ClassRows(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal memuat data kelas.", http.StatusInternalServerError)
		return
	}
	students, err := p.server.StudentCount(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal menghitung mahasiswa.", http.StatusInternalServerError)
		return
	}
	templates, err := p.server.TemplateCount(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal menghitung profil wajah.", http.StatusInternalServerError)
		return
	}
	statuses, err := p.server.ScanStatusCounts(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal menghitung hasil scan.", http.StatusInternalServerError)
		return
	}
	roster, err := p.server.Roster(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal memuat roster mahasiswa.", http.StatusInternalServerError)
		return
	}
	deviceOnline, deviceLastSeen, err := p.server.DeviceStatus(ctx)
	if err != nil {
		http.Error(w, "Gagal membaca status perangkat.", http.StatusInternalServerError)
		return
	}
	faceOnline, faceProvider := p.server.FaceStatus(ctx)

	data := dashboardFragmentData{
		Students: students, Classes: len(classes), ClassCodes: classCodes(classes),
		Verified: statuses["Hadir"], Review: statuses["Perlu ditinjau"], FaceTemplates: templates,
		ClassRows: classes, DeviceOnline: deviceOnline, DeviceLastSeen: deviceLastSeen,
		FaceOnline: faceOnline, FaceProvider: faceProvider,
	}
	for _, row := range classes {
		data.PresentTotal += row.Present
		data.StudentTotal += row.StudentCount
	}
	for index, entry := range roster {
		if index >= 8 {
			break
		}
		data.Rows = append(data.Rows, dashboardScanRow{
			StudentName: entry.StudentName, StudentNIM: entry.StudentNIM, ClassID: entry.ClassID,
			ScannedAt: clock(entry.ScannedAt), Verification: verificationLabel(entry),
			Status: entry.Status, DeviceID: entry.DeviceID,
		})
	}
	p.render(w, "dashboard_fragment", data)
}

func classCodes(classes []api.ClassRow) string {
	codes := make([]string, 0, len(classes))
	for _, row := range classes {
		codes = append(codes, row.ID)
	}
	return strings.Join(codes, " · ")
}

func clock(value *string) string {
	if value == nil || *value == "" {
		return "--:--"
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return "--:--"
	}
	return parsed.Local().Format("15:04")
}

func verificationLabel(entry api.RosterEntry) string {
	switch {
	case entry.FaceVerified:
		return "Terverifikasi"
	case entry.FaceEnrolled:
		return "Terdaftar"
	default:
		return "Belum terdaftar"
	}
}

// initials membuat inisial avatar: huruf pertama dari maksimal dua kata.
func initials(name string) string {
	result := make([]rune, 0, 2)
	for _, field := range strings.Fields(name) {
		for _, r := range field {
			result = append(result, unicode.ToUpper(r))
			break
		}
		if len(result) == 2 {
			break
		}
	}
	return string(result)
}

func statusClass(status string) string {
	switch strings.ToLower(status) {
	case "hadir":
		return "present"
	case "perlu ditinjau":
		return "late"
	case "ditolak":
		return "absent"
	default:
		return ""
	}
}

func formatConfidence(value float64) string {
	if value == 0 {
		return "-"
	}
	return fmt.Sprintf("%.3f", value)
}

type logsFragmentData struct {
	Summary struct {
		Total    int
		Verified int
		Review   int
		Rejected int
	}
	Events     []api.ScanEvent
	EventCount int
	Filtered   bool
	EmptyText  string
}

func (p *Partials) logs(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalOrError(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	classFilter := strings.TrimSpace(r.URL.Query().Get("class"))
	search := strings.TrimSpace(r.URL.Query().Get("q"))

	statuses, err := p.server.AllScanStatusCounts(ctx, principal.Role, principal.ClassIDs)
	if err != nil {
		http.Error(w, "Gagal menghitung status scan.", http.StatusInternalServerError)
		return
	}

	events, err := p.server.AllScanEvents(ctx, principal.Role, principal.ClassIDs, statusFilter, classFilter, search)
	if err != nil {
		http.Error(w, "Gagal memuat log pemindaian.", http.StatusInternalServerError)
		return
	}

	filtered := (statusFilter != "" && statusFilter != "all") || (classFilter != "" && classFilter != "all") || search != ""

	data := logsFragmentData{
		Events:     events,
		EventCount: len(events),
		Filtered:   filtered,
	}
	data.Summary.Total = statuses["Hadir"] + statuses["Perlu ditinjau"] + statuses["Ditolak"]
	data.Summary.Verified = statuses["Hadir"]
	data.Summary.Review = statuses["Perlu ditinjau"]
	data.Summary.Rejected = statuses["Ditolak"]

	if len(events) == 0 {
		if filtered {
			data.EmptyText = "Tidak ada log pemindaian yang cocok dengan filter."
		} else {
			data.EmptyText = "Belum ada log pemindaian."
		}
	}

	p.render(w, "logs_fragment", data)
}
