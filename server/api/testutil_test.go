package main

// Shared test helpers. Every test gets a freshly reset database (schema
// public dropped and re-applied), so tests never see each other's rows.
// Needs TEST_DATABASE_URL; without it the API tests skip.
//
//   docker start league-pg   # postgres:17 on 127.0.0.1:55432 (see the plan)
//   TEST_DATABASE_URL=postgres://postgres:dev@127.0.0.1:55432/league_test go test ./...

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const testAppURL = "https://iamsorenl.github.io/arcade-soccer/"

type sentMail struct{ To, Link string }

// fakeMailer records sign-in emails instead of sending them.
type fakeMailer struct {
	mu   sync.Mutex
	sent []sentMail
}

func (f *fakeMailer) SendLogin(_ context.Context, to, link string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMail{to, link})
	return nil
}

func (f *fakeMailer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// lastToken is the login token from the most recent email's link.
func (f *fakeMailer) lastToken(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("no sign-in email was sent")
	}
	link := f.sent[len(f.sent)-1].Link
	tok, ok := strings.CutPrefix(link, testAppURL+"#login=")
	if !ok || tok == "" {
		t.Fatalf("link %q is not %s#login=<token>", link, testAppURL)
	}
	return tok
}

func newTestApp(t *testing.T) (*App, *fakeMailer) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "drop schema if exists public cascade; create schema public"); err != nil {
		t.Fatal(err)
	}
	if err := ApplySchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	mail := &fakeMailer{}
	app := NewApp(pool, mail, Config{
		AppURL:         testAppURL,
		AllowedOrigins: []string{"https://iamsorenl.github.io"},
	})
	return app, mail
}

// do sends one request through the app's router. token "" = no auth header.
func do(t *testing.T, app *App, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	app.Routes().ServeHTTP(rr, req)
	return rr
}

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return v
}

func wantStatus(t *testing.T, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rr.Code != want {
		t.Fatalf("status %d, want %d; body %s", rr.Code, want, rr.Body.String())
	}
}

func wantError(t *testing.T, rr *httptest.ResponseRecorder, status int, msg string) {
	t.Helper()
	wantStatus(t, rr, status)
	if got := decode[struct{ Error string }](t, rr).Error; got != msg {
		t.Fatalf("error %q, want %q", got, msg)
	}
}

type userJSON struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// signIn runs the whole magic-link flow for email and returns the session.
func signIn(t *testing.T, app *App, mail *fakeMailer, email string) (session string, user userJSON) {
	t.Helper()
	wantStatus(t, do(t, app, "POST", "/auth/start", "", map[string]string{"email": email}), http.StatusOK)
	rr := do(t, app, "POST", "/auth/verify", "", map[string]string{"token": mail.lastToken(t)})
	wantStatus(t, rr, http.StatusOK)
	v := decode[struct {
		Session string   `json:"session"`
		User    userJSON `json:"user"`
	}](t, rr)
	if v.Session == "" {
		t.Fatal("verify returned no session")
	}
	return v.Session, v.User
}
