package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"dash.algorithmics/internal/security"

	"dash.algorithmics/internal/api"
	"dash.algorithmics/internal/controller"
	"dash.algorithmics/internal/store"
	"github.com/gorilla/csrf"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("load .env: %v", err)
	}

	config := api.ParseConfig()
	csrfSecret := os.Getenv("CSRF_SECRET")
	if len(csrfSecret) < 32 {
		log.Fatal("CSRF_SECRET must contain at least 32 characters")
	}
	if len(config.DeviceToken) < 32 {
		log.Fatal("DEVICE_TOKEN must contain at least 32 characters")
	}
	if config.Environment == "production" && len(config.FaceServiceToken) < 32 {
		log.Fatal("FACE_SERVICE_TOKEN must contain at least 32 characters in production")
	}

	mongoURI := env("MONGODB_URI", "mongodb://10.100.1.11:27017")
	mongoDB := env("MONGODB_DATABASE", "attendance")
	database, err := store.Open(mongoURI, mongoDB)
	if err != nil {
		log.Fatalf("initialize database (%s): %v", mongoURI, err)
	}
	defer database.Close()
	log.Printf("connected to MongoDB at %s (database: %s)", mongoURI, mongoDB)
	bootstrapStaff(database)

	staticDir := env("STATIC_DIR", "static")
	viewDir := env("VIEW_DIR", "views")
	web, err := controller.NewWeb(viewDir)
	if err != nil {
		log.Fatalf("load page templates: %v", err)
	}
	sessions := security.NewSessionManager(config.Environment == "production")
	authController := controller.NewAuth(database, sessions, web)
	apiServer := api.New(database, config)
	partials, err := controller.NewPartials(apiServer, viewDir)
	if err != nil {
		log.Fatalf("load fragment templates: %v", err)
	}

	protected := http.NewServeMux()
	protected.HandleFunc("GET /login", authController.LoginPage)
	protected.Handle("POST /login", security.LoginRateLimit(10, 5*time.Minute)(http.HandlerFunc(authController.Login)))
	protected.Handle("POST /logout", security.RequireSession(sessions, database, false)(http.HandlerFunc(authController.Logout)))
	protected.Handle("GET /{$}", security.RequireSession(sessions, database, false)(http.HandlerFunc(web.Dashboard)))
	protected.Handle("GET /mahasiswa", security.RequireSession(sessions, database, false)(http.HandlerFunc(web.Students)))
	protected.Handle("GET /kelas", security.RequireSession(sessions, database, false)(http.HandlerFunc(web.Classes)))
	protected.Handle("GET /laporan", security.RequireSession(sessions, database, false)(http.HandlerFunc(web.Reports)))
	protected.Handle("GET /logs", security.RequireSession(sessions, database, false)(http.HandlerFunc(web.Logs)))
	protected.Handle("GET /akses", security.RequireSession(sessions, database, false)(security.RequireRole("administrator")(http.HandlerFunc(web.Access))))
	protected.Handle("/static/", apiServer.StaticHandler(staticDir))
	protected.Handle("/api/v1/", security.RequireSession(sessions, database, true)(apiServer.StaffHandler()))
	protected.Handle("/partials/", security.RequireSession(sessions, database, true)(partials.Handler()))

	csrfHandler := csrf.Protect(
		[]byte(csrfSecret),
		csrf.Secure(config.Environment == "production"),
		csrf.Path("/"),
		csrf.FieldName("csrf_token"),
		csrf.RequestHeader("X-CSRF-Token"),
		csrf.CookieName("itbjh_csrf"),
		csrf.SameSite(csrf.SameSiteLaxMode),
	)(protected)

	router := http.NewServeMux()
	router.Handle("POST /api/v1/scans", apiServer.DeviceHandler())
	router.Handle("GET /api/v1/health", apiServer.DeviceHandler())
	router.Handle("GET /api/v1/devices/ping", apiServer.DeviceHandler())
	router.Handle("POST /api/v1/devices/ping", apiServer.DeviceHandler())
	router.Handle("/", sessions.LoadAndSave(csrfHandler))
	address := env("HTTP_ADDR", ":8080")
	server := &http.Server{
		Addr:              address,
		Handler:           security.Recover(security.SecurityHeaders(router)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	stop, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignal()
	go func() {
		<-stop.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	}()

	log.Printf("attendance dashboard listening on %s (%s mode)", address, config.Environment)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func bootstrapStaff(database *store.Database) {
	ctx := context.Background()
	adminCount, err := database.CountUsersByRole(ctx, "administrator")
	if err != nil {
		log.Fatalf("count administrator users: %v", err)
	}
	if adminCount == 0 {
		username := strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_USERNAME"))
		password := os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
		if username == "" || len(password) < 12 {
			log.Fatal("first startup requires BOOTSTRAP_ADMIN_USERNAME and BOOTSTRAP_ADMIN_PASSWORD (12+ chars)")
		}
		if err := database.CreateUser(ctx, username, password, username, "administrator", nil); err != nil {
			log.Fatalf("create bootstrap administrator: %v", err)
		}
		log.Printf("bootstrap administrator created; unset bootstrap secrets after initial setup")
	}
	lecturerUsername := strings.TrimSpace(os.Getenv("BOOTSTRAP_LECTURER_USERNAME"))
	lecturerPassword := os.Getenv("BOOTSTRAP_LECTURER_PASSWORD")
	if lecturerUsername != "" && lecturerPassword != "" {
		if len(lecturerPassword) < 12 {
			log.Fatal("BOOTSTRAP_LECTURER_PASSWORD must contain at least 12 characters")
		}
		classes := splitCSV(env("BOOTSTRAP_LECTURER_CLASSES", "IK24-A,IK24-B"))
		if err := database.EnsureUser(ctx, lecturerUsername, lecturerPassword, lecturerUsername, "dosen", classes); err != nil {
			log.Fatalf("create bootstrap lecturer: %v", err)
		}
	}
}

func splitCSV(value string) []string {
	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
