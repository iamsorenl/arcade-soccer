package main

import (
	"encoding/json"
	"log"
	"net/http"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config is everything the server reads from its environment (see main.go).
type Config struct {
	DatabaseURL, AppURL, SimURL, MailFrom, SMTPHost, SMTPUser, SMTPPass string
	SMTPPort                                                            int
	AllowedOrigins                                                      []string
	TrustProxy                                                          bool
	Port                                                                string
}

// App holds the shared pieces every handler needs.
type App struct {
	DB    *pgxpool.Pool
	Mail  Mailer
	Cfg   Config
	limit *limiter
}

func NewApp(db *pgxpool.Pool, mail Mailer, cfg Config) *App {
	return &App{DB: db, Mail: mail, Cfg: cfg, limit: newLimiter()}
}

// Routes maps "METHOD /path" patterns to handlers.
func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /auth/start", a.handleStart)
	mux.HandleFunc("POST /auth/verify", a.handleVerify)
	mux.HandleFunc("POST /auth/signout", a.requireUser(a.handleSignout))
	mux.HandleFunc("GET /me", a.requireUser(a.handleMe))
	return a.cors(mux)
}

// cors lets the allowed origins (the GitHub Pages site) call the API from the
// browser, and answers their preflight OPTIONS requests.
func (a *App) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && slices.Contains(a.Cfg.AllowedOrigins, origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			h.Set("Access-Control-Max-Age", "600")
			h.Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// serverErr logs the real error and sends the player a generic 500.
func serverErr(w http.ResponseWriter, err error) {
	log.Printf("server error: %v", err)
	writeErr(w, http.StatusInternalServerError, "server error")
}

// writeErr sends {"error": msg}, the shape net.js shows to players.
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
