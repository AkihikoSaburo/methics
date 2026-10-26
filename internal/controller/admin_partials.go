package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"dash.algorithmics/internal/api"
	"dash.algorithmics/internal/security"
	"dash.algorithmics/internal/store"
)

// Fragment administrator: hapus mahasiswa (/mahasiswa) dan kelola akun dosen
// (/akses). Semua handler menolak non-administrator dengan 403.

func requireAdminPartial(w http.ResponseWriter, r *http.Request) (security.Principal, bool) {
	principal, ok := principalOrError(w, r)
	if !ok {
		return principal, false
	}
	if principal.Role != "administrator" {
		http.Error(w, "Hanya administrator yang dapat melakukan aksi ini.", http.StatusForbidden)
		return principal, false
	}
	return principal, true
}

// deleteStudent: DELETE /partials/mahasiswa/{nim} → roster terbaru.
func (p *Partials) deleteStudent(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireAdminPartial(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	nim := strings.ToUpper(strings.TrimSpace(r.PathValue("nim")))
	var notice, errMessage string
	name, err := p.server.DeleteStudent(ctx, nim)
	switch {
	case errors.Is(err, api.ErrStudentNotFound):
		errMessage = "Mahasiswa tidak ditemukan; mungkin sudah dihapus."
	case err != nil:
		errMessage = "Mahasiswa gagal dihapus."
	default:
		notice = fmt.Sprintf("Mahasiswa %s (%s) beserta template wajah, foto, dan riwayat scannya telah dihapus.", name, nim)
	}
	data, err := p.mahasiswaRoster(ctx, principal, r.FormValue("q"), r.FormValue("class"), notice, errMessage)
	if err != nil {
		http.Error(w, "Gagal memuat roster mahasiswa.", http.StatusInternalServerError)
		return
	}
	p.render(w, "mahasiswa_fragment", data)
}

type staffRow struct {
	ID          string
	Username    string
	DisplayName string
	Role        string
	Classes     string
	Active      bool
	IsSelf      bool
}

type classOption struct {
	ID       string
	Name     string
	Selected bool
}

type staffFragmentData struct {
	Rows        []staffRow
	Classes     []classOption
	MinPassword int
	Notice      string
	Error       string
	Form        struct {
		Username    string
		DisplayName string
	}
}

func (p *Partials) staffData(ctx context.Context, principal security.Principal, selected []string) (staffFragmentData, error) {
	data := staffFragmentData{MinPassword: store.MinPasswordLength}
	accounts, err := p.server.StaffAccounts(ctx)
	if err != nil {
		return data, err
	}
	for _, a := range accounts {
		classes := strings.Join(a.ClassIDs, ", ")
		if a.Role == "administrator" {
			classes = "Semua kelas"
		} else if classes == "" {
			classes = "-"
		}
		data.Rows = append(data.Rows, staffRow{
			ID: a.ID, Username: a.Username, DisplayName: a.DisplayName, Role: a.Role,
			Classes: classes, Active: a.Active, IsSelf: a.ID == principal.UserID,
		})
	}
	classRows, err := p.server.ClassRows(ctx, "administrator", nil)
	if err != nil {
		return data, err
	}
	chosen := map[string]bool{}
	for _, id := range selected {
		chosen[strings.ToUpper(id)] = true
	}
	for _, c := range classRows {
		data.Classes = append(data.Classes, classOption{ID: c.ID, Name: c.Name, Selected: chosen[c.ID]})
	}
	return data, nil
}

func (p *Partials) renderStaff(w http.ResponseWriter, r *http.Request, principal security.Principal, selected []string, mutate func(*staffFragmentData)) {
	data, err := p.staffData(r.Context(), principal, selected)
	if err != nil {
		http.Error(w, "Gagal memuat akun staf.", http.StatusInternalServerError)
		return
	}
	if mutate != nil {
		mutate(&data)
	}
	p.render(w, "staff_fragment", data)
}

// staff: GET /partials/akses/staff
func (p *Partials) staff(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireAdminPartial(w, r)
	if !ok {
		return
	}
	p.renderStaff(w, r, principal, nil, nil)
}

// createLecturer: POST /partials/akses/lecturers
func (p *Partials) createLecturer(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireAdminPartial(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulir tidak valid.", http.StatusBadRequest)
		return
	}
	input := api.LecturerInput{
		Username:    r.PostFormValue("username"),
		DisplayName: r.PostFormValue("displayName"),
		Password:    r.PostFormValue("password"),
		ClassIDs:    r.PostForm["classIds"],
	}
	if err := p.server.CreateLecturer(r.Context(), input); err != nil {
		// Gagal validasi: tampilkan pesan di form (HTTP 200) dan pertahankan isian
		// kecuali password.
		p.renderStaff(w, r, principal, input.ClassIDs, func(d *staffFragmentData) {
			d.Error = api.LecturerErrorMessage(err)
			d.Form.Username = input.Username
			d.Form.DisplayName = input.DisplayName
		})
		return
	}
	username := strings.ToLower(strings.TrimSpace(input.Username))
	p.renderStaff(w, r, principal, nil, func(d *staffFragmentData) {
		d.Notice = fmt.Sprintf("Akun dosen %q berhasil dibuat. Dosen dapat login dengan username dan password tersebut.", username)
	})
}

// deleteStaff: DELETE /partials/akses/staff/{id}
func (p *Partials) deleteStaff(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireAdminPartial(w, r)
	if !ok {
		return
	}
	err := p.server.DeleteLecturer(r.Context(), r.PathValue("id"))
	p.renderStaff(w, r, principal, nil, func(d *staffFragmentData) {
		if err != nil {
			d.Error = api.LecturerErrorMessage(err)
		} else {
			d.Notice = "Akun dosen telah dihapus; sesinya langsung berakhir."
		}
	})
}
