package main

// Task 3: the API core and magic-link sign-in. These fail to compile until
// you write App, NewApp, Config, ApplySchema, Routes, Mailer and hashToken.
// Make them pass one at a time, top to bottom.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	app, _ := newTestApp(t)
	wantStatus(t, do(t, app, "GET", "/healthz", "", nil), http.StatusOK)
}

func TestCORS(t *testing.T) {
	app, _ := newTestApp(t)
	preflight := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("OPTIONS", "/me", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
		rr := httptest.NewRecorder()
		app.Routes().ServeHTTP(rr, req)
		return rr
	}

	ok := preflight("https://iamsorenl.github.io")
	if got := ok.Header().Get("Access-Control-Allow-Origin"); got != "https://iamsorenl.github.io" {
		t.Fatalf("allowed origin: ACAO = %q", got)
	}
	allow := strings.ToLower(ok.Header().Get("Access-Control-Allow-Headers"))
	for _, h := range []string{"authorization", "content-type"} {
		if !strings.Contains(allow, h) {
			t.Fatalf("Access-Control-Allow-Headers %q is missing %s", allow, h)
		}
	}

	evil := preflight("https://evil.example")
	if got := evil.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unknown origin got ACAO %q", got)
	}
}

func TestStartSendsLink(t *testing.T) {
	app, mail := newTestApp(t)
	rr := do(t, app, "POST", "/auth/start", "", map[string]string{"email": "a@b.co"})
	wantStatus(t, rr, http.StatusOK)
	if !decode[struct{ Sent bool }](t, rr).Sent {
		t.Fatal(`want {"sent": true}`)
	}
	if mail.count() != 1 || mail.sent[0].To != "a@b.co" {
		t.Fatalf("sent %+v, want one email to a@b.co", mail.sent)
	}
	mail.lastToken(t) // link is APP_URL#login=<token>
}

func TestStartBadEmail(t *testing.T) {
	app, mail := newTestApp(t)
	for _, email := range []string{"", "   ", "not-an-email"} {
		wantStatus(t, do(t, app, "POST", "/auth/start", "", map[string]string{"email": email}), http.StatusBadRequest)
	}
	wantStatus(t, do(t, app, "POST", "/auth/start", "", "{not json"), http.StatusBadRequest)
	if mail.count() != 0 {
		t.Fatal("bad input sent an email")
	}
}

func TestStartNormalizesEmail(t *testing.T) {
	app, mail := newTestApp(t)
	// A migrated account: same person, typed differently this time.
	const existing = "00000000-0000-0000-0000-0000000000aa"
	if _, err := app.DB.Exec(context.Background(),
		"insert into users (id, email) values ($1, 'a@b.co')", existing); err != nil {
		t.Fatal(err)
	}
	_, user := signIn(t, app, mail, "  A@B.co ")
	if user.ID != existing || user.Email != "a@b.co" {
		t.Fatalf("signed in as %+v, want the existing account %s", user, existing)
	}
	if mail.sent[0].To != "a@b.co" {
		t.Fatalf("mailed %q, want the normalized address", mail.sent[0].To)
	}
}

func TestStartRateLimit(t *testing.T) {
	app, mail := newTestApp(t)
	for range 3 {
		wantStatus(t, do(t, app, "POST", "/auth/start", "", map[string]string{"email": "a@b.co"}), http.StatusOK)
	}
	wantError(t, do(t, app, "POST", "/auth/start", "", map[string]string{"email": "A@b.co"}),
		http.StatusTooManyRequests, "too many sign-in emails, try again later")
	if mail.count() != 3 {
		t.Fatalf("sent %d emails, want 3", mail.count())
	}
}

func TestStartRateLimitPerIP(t *testing.T) {
	app, _ := newTestApp(t)
	// httptest requests all come from 192.0.2.1: 10 per IP per hour.
	for i := range 10 {
		wantStatus(t, do(t, app, "POST", "/auth/start", "",
			map[string]string{"email": fmt.Sprintf("u%d@b.co", i)}), http.StatusOK)
	}
	wantError(t, do(t, app, "POST", "/auth/start", "", map[string]string{"email": "u99@b.co"}),
		http.StatusTooManyRequests, "too many sign-in emails, try again later")
}

func TestVerifyTokenSingleUse(t *testing.T) {
	app, mail := newTestApp(t)
	wantStatus(t, do(t, app, "POST", "/auth/start", "", map[string]string{"email": "a@b.co"}), http.StatusOK)
	tok := mail.lastToken(t)

	first := do(t, app, "POST", "/auth/verify", "", map[string]string{"token": tok})
	wantStatus(t, first, http.StatusOK)
	session := decode[struct{ Session string }](t, first).Session

	wantError(t, do(t, app, "POST", "/auth/verify", "", map[string]string{"token": tok}),
		http.StatusBadRequest, "link expired or already used, request a new one")
	// The failed second use must not have touched the first session.
	wantStatus(t, do(t, app, "GET", "/me", session, nil), http.StatusOK)
}

func TestVerifyExpired(t *testing.T) {
	app, _ := newTestApp(t)
	if _, err := app.DB.Exec(context.Background(),
		"insert into login_tokens (token_hash, email, expires_at) values ($1, 'a@b.co', now() - interval '1 minute')",
		hashToken("expired-token")); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{"expired-token", "never-issued", ""} {
		wantError(t, do(t, app, "POST", "/auth/verify", "", map[string]string{"token": tok}),
			http.StatusBadRequest, "link expired or already used, request a new one")
	}
}

func TestMe(t *testing.T) {
	app, mail := newTestApp(t)
	session, user := signIn(t, app, mail, "a@b.co")
	rr := do(t, app, "GET", "/me", session, nil)
	wantStatus(t, rr, http.StatusOK)
	me := decode[struct {
		User    userJSON        `json:"user"`
		Profile json.RawMessage `json:"profile"`
		Team    json.RawMessage `json:"team"`
	}](t, rr)
	if me.User != user {
		t.Fatalf("/me user %+v, want %+v", me.User, user)
	}
	if string(me.Profile) != "null" || string(me.Team) != "null" {
		t.Fatalf("new user: want profile and team null, got %s / %s", me.Profile, me.Team)
	}
}

func TestMeRequiresSession(t *testing.T) {
	app, mail := newTestApp(t)
	_, user := signIn(t, app, mail, "a@b.co")
	if _, err := app.DB.Exec(context.Background(),
		"insert into sessions (token_hash, user_id, expires_at) values ($1, $2, now() - interval '1 second')",
		hashToken("stale-session"), user.ID); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{"", "garbage", "stale-session"} {
		wantError(t, do(t, app, "GET", "/me", tok, nil), http.StatusUnauthorized, "not signed in")
	}
}

func TestSignout(t *testing.T) {
	app, mail := newTestApp(t)
	session, _ := signIn(t, app, mail, "a@b.co")
	wantStatus(t, do(t, app, "POST", "/auth/signout", session, nil), http.StatusOK)
	wantError(t, do(t, app, "GET", "/me", session, nil), http.StatusUnauthorized, "not signed in")
}
