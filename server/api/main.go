package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func configFromEnv() Config {
	port, _ := strconv.Atoi(env("SMTP_PORT", "587"))
	return Config{
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		AppURL:         env("APP_URL", "http://localhost:8000/"),
		SimURL:         env("SIM_URL", "http://127.0.0.1:8081"),
		MailFrom:       env("MAIL_FROM", "Arcade Soccer <"+os.Getenv("SMTP_USER")+">"),
		SMTPHost:       os.Getenv("SMTP_HOST"),
		SMTPUser:       os.Getenv("SMTP_USER"),
		SMTPPass:       os.Getenv("SMTP_PASS"),
		SMTPPort:       port,
		AllowedOrigins: strings.Split(env("ALLOWED_ORIGINS", "http://localhost:8000"), ","),
		TrustProxy:     os.Getenv("TRUST_PROXY") == "true",
		Port:           env("PORT", "8080"),
	}
}

func main() {
	cfg := configFromEnv()
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := ApplySchema(ctx, pool); err != nil {
		log.Fatalf("apply schema: %v", err)
	}

	var mail Mailer = logMailer{}
	if cfg.SMTPHost != "" {
		mail = smtpMailer{host: cfg.SMTPHost, port: cfg.SMTPPort, user: cfg.SMTPUser, pass: cfg.SMTPPass, from: cfg.MailFrom}
	} else {
		log.Print("SMTP_HOST not set: sign-in links are printed here instead of emailed")
	}

	app := NewApp(pool, mail, cfg)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           app.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      60 * time.Second, // placement runs 5 sims
	}
	log.Printf("league api on :%s", cfg.Port)
	log.Fatal(srv.ListenAndServe())
}
