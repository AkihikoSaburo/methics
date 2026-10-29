package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"dash.algorithmics/internal/api"
)

func newTestPartials(t *testing.T) *Partials {
	t.Helper()
	p, err := NewPartials(nil, "../../views")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStaffFragment(t *testing.T) {
	p := newTestPartials(t)
	data := staffFragmentData{
		MinPassword: 4,
		Rows: []staffRow{
			{ID: "a1", Username: "admin", DisplayName: "admin", Role: "administrator", Classes: "Semua kelas", Active: true, IsSelf: true},
			{ID: "d1", Username: "dosen", DisplayName: "Dosen <Satu>", Role: "dosen", Classes: "IK24-A", Active: true},
		},
		Classes: []classOption{{ID: "IK24-A", Name: "Ilmu Komputer A", Selected: true}, {ID: "IK24-B", Name: "Ilmu Komputer B"}},
		Error:   "Username sudah dipakai.",
	}
	rec := httptest.NewRecorder()
	p.render(rec, "staff_fragment", data)
	body := rec.Body.String()
	for _, want := range []string{
		`hx-delete="/partials/akses/staff/d1"`, `hx-post="/partials/akses/lecturers"`,
		`value="IK24-A" checked`, "Username sudah dipakai.", "Dosen &lt;Satu&gt;", "(Anda)", `minlength="4"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, `/partials/akses/staff/a1"`) {
		t.Fatal("administrator must not get a delete button")
	}
}

func TestRosterDeleteButtonAdminOnly(t *testing.T) {
	p := newTestPartials(t)
	row := mahasiswaRow{StudentNIM: "241011001", StudentName: "Alya Putri", ClassID: "IK24-A", ClassName: "Ilmu Komputer A", FaceEnrolled: true, Status: "Belum hadir"}
	for role, want := range map[string]bool{"administrator": true, "dosen": false} {
		rec := httptest.NewRecorder()
		p.render(rec, "mahasiswa_fragment", mahasiswaFragmentData{Role: role, Total: 1, Rows: []mahasiswaRow{row}})
		has := strings.Contains(rec.Body.String(), `hx-delete="/partials/mahasiswa/241011001"`)
		if has != want {
			t.Fatalf("role %s: delete button present=%v", role, has)
		}
	}
	_ = api.ErrStudentNotFound
}

func TestRosterViewPhotoLink(t *testing.T) {
	p := newTestPartials(t)
	withPhoto := mahasiswaRow{StudentNIM: "241011001", StudentName: "Alya Putri", ClassID: "IK24-A", FaceEnrolled: true,
		FaceImageURL: "/api/v1/images/enroll/student-241011001/a.jpg"}
	noPhoto := mahasiswaRow{StudentNIM: "241011002", StudentName: "Bima Saputra", ClassID: "IK24-A", FaceEnrolled: true}
	for _, role := range []string{"administrator", "dosen"} {
		rec := httptest.NewRecorder()
		p.render(rec, "mahasiswa_fragment", mahasiswaFragmentData{Role: role, Total: 2, Rows: []mahasiswaRow{withPhoto, noPhoto}})
		body := rec.Body.String()
		if n := strings.Count(body, "Lihat foto"); n != 1 {
			t.Fatalf("role %s: want 1 Lihat foto link, got %d", role, n)
		}
		if !strings.Contains(body, `href="/api/v1/images/enroll/student-241011001/a.jpg"`) {
			t.Fatalf("role %s: photo href missing", role)
		}
	}
}

func TestRosterMultiPhotoEnrollment(t *testing.T) {
	p := newTestPartials(t)
	row := mahasiswaRow{StudentNIM: "241011003", StudentName: "Citra Nabila", ClassID: "IK24-B", FaceEnrolled: true, FaceTemplates: 3,
		FaceImageURL:  "/api/v1/images/enroll/s/1.jpg",
		FaceImageURLs: []string{"/api/v1/images/enroll/s/1.jpg", "/api/v1/images/enroll/s/2.jpg", "/api/v1/images/enroll/s/3.jpg"}}
	rec := httptest.NewRecorder()
	p.render(rec, "mahasiswa_fragment", mahasiswaFragmentData{Role: "administrator", Total: 1, Rows: []mahasiswaRow{row}})
	body := rec.Body.String()
	for _, want := range []string{`name="images"`, "multiple", "Enroll ulang", "Template aktif (3 pose)",
		`href="/api/v1/images/enroll/s/2.jpg"`, `href="/api/v1/images/enroll/s/3.jpg"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}
