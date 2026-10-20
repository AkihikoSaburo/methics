package model

import "time"

type Page struct {
	Title       string
	PageLabel   string
	Page        string
	Active      string
	Role        string
	RoleName    string
	UserName    string
	TeacherName string
	RoleClasses string
	CSRFToken   string
	LoginError  string
	NextURL     string
	Today       string
}

func NewPage(page, role string, userName, roleClasses, csrfToken string) Page {
	pageTitles := map[string]string{
		"dashboard": "Dashboard absensi",
		"mahasiswa": "Mahasiswa",
		"kelas":     "Kelas",
		"laporan":   "Laporan absensi",
		"akses":     "Akses & pengguna",
	}
	roleNames := map[string]string{
		"administrator": "Administrator",
		"dosen":         "Dosen",
	}
	if _, ok := pageTitles[page]; !ok {
		page = "dashboard"
	}
	if _, ok := roleNames[role]; !ok {
		role = "administrator"
	}
	now := time.Now()
	weekdays := []string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}
	months := []string{"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"}
	return Page{
		Title:       pageTitles[page] + " | Institut Teknologi Bacharuddin Jusuf Habibie",
		PageLabel:   pageTitles[page],
		Page:        page,
		Active:      page,
		Role:        role,
		RoleName:    roleNames[role],
		UserName:    userName,
		TeacherName: "Dr. Rina Wibowo",
		RoleClasses: roleClasses,
		CSRFToken:   csrfToken,
		Today:       weekdays[now.Weekday()] + ", " + now.Format("02") + " " + months[now.Month()-1] + " " + now.Format("2006"),
	}
}
