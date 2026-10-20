package controller

import (
	"html/template"
	"net/http"
	"path/filepath"
	"strings"

	"dash.algorithmics/internal/model"
	"dash.algorithmics/internal/security"
	"github.com/gorilla/csrf"
)

type Web struct {
	templates map[string]*template.Template
}

func NewWeb(viewDir string) (*Web, error) {
	pages := []string{"dashboard", "mahasiswa", "kelas", "laporan", "akses"}
	views := make(map[string]*template.Template, len(pages))
	layoutPath := filepath.Join(viewDir, "layouts", "base.html")
	sidebarPath := filepath.Join(viewDir, "partials", "sidebar.html")
	topbarPath := filepath.Join(viewDir, "partials", "topbar.html")
	toastPath := filepath.Join(viewDir, "partials", "toast.html")
	for _, page := range pages {
		parsed, err := template.ParseFiles(
			layoutPath,
			sidebarPath,
			topbarPath,
			toastPath,
			filepath.Join(viewDir, "pages", page+".html"),
		)
		if err != nil {
			return nil, err
		}
		views[page] = parsed
	}
	login, err := template.ParseFiles(filepath.Join(viewDir, "login.html"))
	if err != nil {
		return nil, err
	}
	views["login"] = login
	return &Web{templates: views}, nil
}

func (web *Web) Dashboard(w http.ResponseWriter, r *http.Request) {
	web.render(w, r, "dashboard")
}

func (web *Web) Students(w http.ResponseWriter, r *http.Request) {
	web.render(w, r, "mahasiswa")
}

func (web *Web) Classes(w http.ResponseWriter, r *http.Request) {
	web.render(w, r, "kelas")
}

func (web *Web) Reports(w http.ResponseWriter, r *http.Request) {
	web.render(w, r, "laporan")
}

func (web *Web) Access(w http.ResponseWriter, r *http.Request) {
	web.render(w, r, "akses")
}

func (web *Web) render(w http.ResponseWriter, r *http.Request, pageName string) {
	user, ok := security.PrincipalFromContext(r.Context())
	if !ok {
		http.Error(w, "Autentikasi pengguna diperlukan.", http.StatusUnauthorized)
		return
	}
	if pageName == "akses" && user.Role != "administrator" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	roleClasses := strings.Join(user.ClassIDs, ",")
	data := model.NewPage(pageName, user.Role, user.DisplayName, roleClasses, csrf.Token(r))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := web.templates[pageName].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "Gagal merender halaman.", http.StatusInternalServerError)
	}
}

func (web *Web) LoginPage(w http.ResponseWriter, r *http.Request, errorMessage, nextURL string) {
	data := model.NewPage("dashboard", "administrator", "", "", csrf.Token(r))
	data.Title = "Login | Institut Teknologi Bacharuddin Jusuf Habibie"
	data.PageLabel = "Login"
	data.LoginError = errorMessage
	data.NextURL = nextURL
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := web.templates["login"].ExecuteTemplate(w, "login", data); err != nil {
		http.Error(w, "Gagal merender halaman login.", http.StatusInternalServerError)
	}
}
