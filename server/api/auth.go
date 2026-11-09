package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	loginTTL      = 15 * time.Minute
	emailLimit    = 3 // sign-in emails per address per 15 min
	emailWindow   = 15 * time.Minute
	ipLimit       = 10 // sign-in emails per client IP per hour
	ipWindow      = time.Hour
	msgRateLimit  = "too many sign-in emails, try again later"
	msgBadLink    = "link expired or already used, request a new one"
	msgNotSignedI = "not signed in"
)

// hashToken is what the database stores instead of the raw token.
func hashToken(tok string) []byte {
	sum := sha256.Sum256([]byte(tok))
	return sum[:]
}

// newToken is 32 random bytes, URL-safe so it can ride in #login=.
func newToken() string {
	b := make([]byte, 32)
	rand.Read(b) // never returns an error on supported platforms
	return base64.RawURLEncoding.EncodeToString(b)
}

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// limiter is an in-memory sliding window per key. It resets on restart, which
// is fine for one replica; a second replica would need it in Postgres.
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{hits: map[string][]time.Time{}} }

// allow records a hit for key and reports whether it is within max per window.
func (l *limiter) allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	recent := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= max {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}

// clientIP is the first X-Forwarded-For entry when behind a trusted proxy,
// else the TCP peer address.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// readJSON decodes a small JSON body into v; false means it already sent a 400.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func (a *App) handleStart(w http.ResponseWriter, r *http.Request) {
	var body struct{ Email string }
	if !readJSON(w, r, &body) {
		return
	}
	email := normalizeEmail(body.Email)
	if len(email) > 254 || !strings.Contains(email, "@") {
		writeErr(w, http.StatusBadRequest, "enter a valid email")
		return
	}
	if !a.limit.allow("ip:"+clientIP(r, a.Cfg.TrustProxy), ipLimit, ipWindow) ||
		!a.limit.allow("email:"+email, emailLimit, emailWindow) {
		writeErr(w, http.StatusTooManyRequests, msgRateLimit)
		return
	}
	tok := newToken()
	if _, err := a.DB.Exec(r.Context(),
		"insert into login_tokens (token_hash, email, expires_at) values ($1, $2, $3)",
		hashToken(tok), email, time.Now().Add(loginTTL)); err != nil {
		serverErr(w, err)
		return
	}
	if err := a.Mail.SendLogin(r.Context(), email, a.Cfg.AppURL+"#login="+tok); err != nil {
		log.Printf("send login email: %v", err)
		writeErr(w, http.StatusInternalServerError, "could not send the email, try again")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

type userOut struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

func (a *App) handleVerify(w http.ResponseWriter, r *http.Request) {
	var body struct{ Token string }
	if !readJSON(w, r, &body) {
		return
	}
	session := newToken()
	var u userOut
	err := pgx.BeginFunc(r.Context(), a.DB, func(tx pgx.Tx) error {
		// Single use: the row is claimed and checked in one statement, so two
		// clicks racing each other can't both win.
		var email string
		if err := tx.QueryRow(r.Context(), `
			update login_tokens set used_at = now()
			where token_hash = $1 and used_at is null and expires_at > now()
			returning email`, hashToken(body.Token)).Scan(&email); err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `
			insert into users (email) values ($1)
			on conflict (email) do update set email = excluded.email
			returning id::text, email`, email).Scan(&u.ID, &u.Email); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `
			insert into sessions (token_hash, user_id, expires_at)
			values ($1, $2, now() + interval '90 days')`, hashToken(session), u.ID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusBadRequest, msgBadLink)
		return
	}
	if err != nil {
		serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session, "user": u})
}

func bearer(r *http.Request) string {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return strings.TrimSpace(tok)
}

// requireUser resolves the bearer session to a user id, or answers 401. The
// id handlers get comes from here only, never from a request body.
func (a *App) requireUser(h func(w http.ResponseWriter, r *http.Request, userID string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			writeErr(w, http.StatusUnauthorized, msgNotSignedI)
			return
		}
		var userID string
		var stale bool
		err := a.DB.QueryRow(r.Context(), `
			select user_id::text, last_seen_at < now() - interval '24 hours'
			from sessions where token_hash = $1 and expires_at > now()`,
			hashToken(tok)).Scan(&userID, &stale)
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusUnauthorized, msgNotSignedI)
			return
		}
		if err != nil {
			serverErr(w, err)
			return
		}
		if stale { // sliding expiry, at most one write per day per session
			if _, err := a.DB.Exec(r.Context(), `
				update sessions set last_seen_at = now(), expires_at = now() + interval '90 days'
				where token_hash = $1`, hashToken(tok)); err != nil {
				log.Printf("extend session: %v", err)
			}
		}
		h(w, r, userID)
	}
}

func (a *App) handleSignout(w http.ResponseWriter, r *http.Request, _ string) {
	if _, err := a.DB.Exec(r.Context(), "delete from sessions where token_hash = $1", hashToken(bearer(r))); err != nil {
		serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"signedOut": true})
}

type profileOut struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type teamOut struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Elo     int    `json:"elo"`
	Version int    `json:"version"`
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request, userID string) {
	ctx := r.Context()
	var out struct {
		User    userOut     `json:"user"`
		Profile *profileOut `json:"profile"`
		Team    *teamOut    `json:"team"`
	}
	if err := a.DB.QueryRow(ctx, "select id::text, email from users where id = $1", userID).
		Scan(&out.User.ID, &out.User.Email); err != nil {
		serverErr(w, err)
		return
	}
	var err error
	if out.Profile, err = a.loadProfile(ctx, userID); err != nil {
		serverErr(w, err)
		return
	}
	if out.Team, err = a.loadTeam(ctx, userID); err != nil {
		serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// loadProfile returns nil (no error) when the user has no profile yet.
func (a *App) loadProfile(ctx context.Context, userID string) (*profileOut, error) {
	var p profileOut
	err := a.DB.QueryRow(ctx, "select id::text, username from profiles where id = $1", userID).
		Scan(&p.ID, &p.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &p, err
}

// loadTeam returns nil (no error) when the user hasn't published a team.
func (a *App) loadTeam(ctx context.Context, userID string) (*teamOut, error) {
	var t teamOut
	err := a.DB.QueryRow(ctx, "select id::text, name, elo, version from teams where owner = $1", userID).
		Scan(&t.ID, &t.Name, &t.Elo, &t.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &t, err
}
