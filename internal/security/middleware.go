package security

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"dash.algorithmics/internal/store"
	"github.com/alexedwards/scs/v2"
)

type principalKey struct{}

// Principal adalah identitas staf yang sedang login, dimuat ulang dari
// koleksi users pada setiap request oleh RequireSession.
type Principal struct {
	UserID      string
	Username    string
	DisplayName string
	Role        string
	ClassIDs    []string
}

type loginWindow struct {
	started time.Time
	count   int
}

var loginAttempts = struct {
	sync.Mutex
	byIP map[string]loginWindow
}{byIP: make(map[string]loginWindow)}

// NewSessionManager membuat session store in-memory (cookie itbjh_session,
// umur 8 jam, idle 30 menit). Restart server mengakhiri semua sesi.
func NewSessionManager(secureCookie bool) *scs.SessionManager {
	manager := scs.New()
	manager.Lifetime = 8 * time.Hour
	manager.IdleTimeout = 30 * time.Minute
	manager.Cookie.Name = "itbjh_session"
	manager.Cookie.Path = "/"
	manager.Cookie.HttpOnly = true
	manager.Cookie.Secure = secureCookie
	manager.Cookie.Persist = true
	manager.Cookie.SameSite = http.SameSiteLaxMode
	return manager
}

// PrincipalFromContext mengambil Principal yang dipasang RequireSession.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

// WithPrincipal memasang Principal ke context request.
func WithPrincipal(r *http.Request, principal Principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalKey{}, principal))
}

// RequireSession mewajibkan sesi staf. User (role, class_ids, status aktif)
// dibaca ulang dari MongoDB setiap request, sehingga akun yang dihapus atau
// dinonaktifkan langsung kehilangan akses. Gagal: 401 JSON bila apiRequest
// (atau path /api/), selain itu redirect ke /login?next=….
func RequireSession(manager *scs.SessionManager, database *store.Database, apiRequest bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := manager.GetString(r.Context(), "user_id")
			if userID == "" {
				unauthorized(w, r, apiRequest)
				return
			}
			user, err := database.UserByID(r.Context(), userID)
			if err != nil {
				manager.Remove(r.Context(), "user_id")
				unauthorized(w, r, apiRequest)
				return
			}
			principal := Principal{
				UserID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
				Role: user.Role, ClassIDs: user.ClassIDs,
			}
			next.ServeHTTP(w, WithPrincipal(r, principal))
		})
	}
}

// RequireRole membatasi handler ke role tertentu: 403 JSON untuk /api/,
// redirect ke / untuk halaman.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized", "Autentikasi staf diperlukan.")
				return
			}
			if _, ok := allowed[principal.Role]; !ok {
				if !strings.HasPrefix(r.URL.Path, "/api/") {
					http.Redirect(w, r, "/", http.StatusSeeOther)
					return
				}
				writeJSONError(w, http.StatusForbidden, "forbidden", "Role tidak diizinkan untuk aksi ini.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders memasang header keamanan (CSP script-src 'self', nosniff,
// X-Frame-Options DENY, dst.) untuk seluruh router, plus Cache-Control:
// no-store untuk semua path kecuali /static/.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "same-origin")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		header.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' https://fonts.googleapis.com 'unsafe-inline'; img-src 'self' https://proxy-assets.etherthink.xyz data:; font-src 'self' https://fonts.gstatic.com data:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'self'")
		if strings.HasPrefix(r.URL.Path, "/") && !strings.HasPrefix(r.URL.Path, "/static/") {
			header.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// LoginRateLimit membatasi percobaan login per IP (in-memory) dalam satu
// jendela waktu; kelebihan → 429 dengan Retry-After.
func LoginRateLimit(maxAttempts int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
			now := time.Now()
			loginAttempts.Lock()
			state := loginAttempts.byIP[ip]
			if state.started.IsZero() || now.Sub(state.started) >= window {
				state = loginWindow{started: now}
			}
			state.count++
			loginAttempts.byIP[ip] = state
			blocked := state.count > maxAttempts
			if len(loginAttempts.byIP) > 10000 {
				for key, entry := range loginAttempts.byIP {
					if now.Sub(entry.started) >= window {
						delete(loginAttempts.byIP, key)
					}
				}
			}
			loginAttempts.Unlock()
			if blocked {
				w.Header().Set("Retry-After", strconv.Itoa(max(1, int(window.Seconds()))))
				http.Error(w, "Terlalu banyak percobaan login. Coba lagi nanti.", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Recover menangkap panic, mencatatnya di log, dan membalas 500 tanpa detail.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("panic serving %s: %v", r.URL.Path, recovered)
				http.Error(w, "Terjadi kesalahan internal.", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func unauthorized(w http.ResponseWriter, r *http.Request, apiRequest bool) {
	if apiRequest || strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "Silakan login untuk melanjutkan.")
		return
	}
	next := r.URL.RequestURI()
	if next == "" || strings.HasPrefix(next, "//") {
		next = "/"
	}
	http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
