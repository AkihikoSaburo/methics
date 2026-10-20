package controller

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"dash.algorithmics/internal/store"
	"github.com/alexedwards/scs/v2"
)

type Auth struct {
	database *store.Database
	sessions *scs.SessionManager
	web      *Web
}

func NewAuth(database *store.Database, sessions *scs.SessionManager, web *Web) *Auth {
	return &Auth{database: database, sessions: sessions, web: web}
}

func (auth *Auth) LoginPage(w http.ResponseWriter, r *http.Request) {
	if auth.sessions.Exists(r.Context(), "user_id") {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	auth.web.LoginPage(w, r, "", safeNext(r.URL.Query().Get("next")))
}

func (auth *Auth) Login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		auth.web.LoginPage(w, r, "Username atau password tidak valid.", "/")
		return
	}
	user, err := auth.database.AuthenticateUser(r.Context(), r.FormValue("username"), r.FormValue("password"))
	if err != nil {
		if errors.Is(err, store.ErrInvalidCredentials) {
			auth.web.LoginPage(w, r, "Username atau password tidak valid.", safeNext(r.FormValue("next")))
			return
		}
		http.Error(w, "Login sementara tidak tersedia.", http.StatusInternalServerError)
		return
	}
	if err := auth.sessions.RenewToken(r.Context()); err != nil {
		http.Error(w, "Login sementara tidak tersedia.", http.StatusInternalServerError)
		return
	}
	auth.sessions.Put(r.Context(), "user_id", user.ID)
	http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusSeeOther)
}

func (auth *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if err := auth.sessions.Destroy(r.Context()); err != nil {
		http.Error(w, "Gagal mengakhiri sesi.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func safeNext(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "/"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return "/"
	}
	return parsed.RequestURI()
}
