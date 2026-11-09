package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type teamJSON struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Elo     int    `json:"elo"`
	Version int    `json:"version"`
}

var cfg = map[string]any{"formation": "433", "press": 3}

// userWithProfile signs in email and creates a profile named username.
func userWithProfile(t *testing.T, app *App, mail *fakeMailer, email, username string) (string, userJSON) {
	t.Helper()
	tok, u := signIn(t, app, mail, email)
	wantStatus(t, do(t, app, "POST", "/profile", tok, map[string]string{"username": username}), http.StatusCreated)
	return tok, u
}

func publish(t *testing.T, app *App, tok, name string) *teamJSON {
	t.Helper()
	rr := do(t, app, "PUT", "/team", tok, map[string]any{"name": name, "config": cfg, "version": 1})
	wantStatus(t, rr, http.StatusOK)
	v := decode[teamJSON](t, rr)
	return &v
}

func TestCreateProfile(t *testing.T) {
	app, mail := newTestApp(t)
	tok, u := signIn(t, app, mail, "a@example.com")
	rr := do(t, app, "POST", "/profile", tok, map[string]string{"username": "alice"})
	wantStatus(t, rr, http.StatusCreated)
	if p := decode[profileOut](t, rr); p.ID != u.ID || p.Username != "alice" {
		t.Fatalf("got %+v, want id %s username alice", p, u.ID)
	}
	// A second profile for the same user is refused.
	wantError(t, do(t, app, "POST", "/profile", tok, map[string]string{"username": "other"}), http.StatusConflict, "profile already exists")
	// Length outside 3-20 is a 400.
	tok2, _ := signIn(t, app, mail, "b@example.com")
	wantStatus(t, do(t, app, "POST", "/profile", tok2, map[string]string{"username": "ab"}), http.StatusBadRequest)
	wantStatus(t, do(t, app, "POST", "/profile", tok2, map[string]string{"username": strings.Repeat("x", 21)}), http.StatusBadRequest)
	wantStatus(t, do(t, app, "POST", "/profile", "", map[string]string{"username": "nobody"}), http.StatusUnauthorized)
}

func TestCreateProfileTaken(t *testing.T) {
	app, mail := newTestApp(t)
	userWithProfile(t, app, mail, "a@example.com", "alice")
	tok2, _ := signIn(t, app, mail, "b@example.com")
	wantError(t, do(t, app, "POST", "/profile", tok2, map[string]string{"username": "alice"}), http.StatusConflict, "username taken")
}

func TestPublishTeamUpsert(t *testing.T) {
	app, mail := newTestApp(t)
	tok, _ := userWithProfile(t, app, mail, "a@example.com", "alice")
	first := publish(t, app, tok, "Reds")
	second := publish(t, app, tok, "Blues")
	if first.ID != second.ID || second.Name != "Blues" || second.Elo != 1000 {
		t.Fatalf("first %+v second %+v", first, second)
	}
	var n int
	if err := app.DB.QueryRow(context.Background(), "select count(*) from teams").Scan(&n); err != nil || n != 1 {
		t.Fatalf("teams rows %d, err %v", n, err)
	}
}

func TestPublishIgnoresElo(t *testing.T) {
	app, mail := newTestApp(t)
	tok, _ := userWithProfile(t, app, mail, "a@example.com", "alice")
	rr := do(t, app, "PUT", "/team", tok, map[string]any{
		"name": "Reds", "config": cfg, "version": 1, "elo": 9999, "wins": 50,
	})
	wantStatus(t, rr, http.StatusOK)
	if got := decode[teamJSON](t, rr).Elo; got != 1000 {
		t.Fatalf("elo %d, want 1000", got)
	}
	var wins int
	if err := app.DB.QueryRow(context.Background(), "select wins from teams").Scan(&wins); err != nil || wins != 0 {
		t.Fatalf("wins %d, err %v", wins, err)
	}
}

func TestPublishOtherUsersTeamImpossible(t *testing.T) {
	app, mail := newTestApp(t)
	tokA, ua := userWithProfile(t, app, mail, "a@example.com", "alice")
	tokB, _ := userWithProfile(t, app, mail, "b@example.com", "bobby")
	a := publish(t, app, tokA, "Alice FC")
	// B names A's team and owner in the body; the server ignores both.
	rr := do(t, app, "PUT", "/team", tokB, map[string]any{
		"id": a.ID, "owner": ua.ID, "name": "Bob FC", "config": cfg, "version": 1,
	})
	wantStatus(t, rr, http.StatusOK)
	if b := decode[teamJSON](t, rr); b.ID == a.ID {
		t.Fatal("B's publish returned A's team")
	}
	var name string
	if err := app.DB.QueryRow(context.Background(), "select name from teams where id = $1", a.ID).Scan(&name); err != nil || name != "Alice FC" {
		t.Fatalf("A's team name %q, err %v", name, err)
	}
}

func TestPublishConfigTooBig(t *testing.T) {
	app, mail := newTestApp(t)
	tok, _ := userWithProfile(t, app, mail, "a@example.com", "alice")
	big := map[string]string{}
	for i := 0; i < 300; i++ { // distinct keys and values, ~9 KB
		big[fmt.Sprintf("k%03d", i)] = fmt.Sprintf("v%025d", i*7919)
	}
	wantStatus(t, do(t, app, "PUT", "/team", tok, map[string]any{"name": "Big", "config": big, "version": 1}), http.StatusBadRequest)
	// A non-object config and bad name lengths are fenced too.
	wantStatus(t, do(t, app, "PUT", "/team", tok, map[string]any{"name": "X", "config": []int{1}, "version": 1}), http.StatusBadRequest)
	wantStatus(t, do(t, app, "PUT", "/team", tok, map[string]any{"name": "", "config": cfg, "version": 1}), http.StatusBadRequest)
	wantStatus(t, do(t, app, "PUT", "/team", tok, map[string]any{"name": strings.Repeat("n", 25), "config": cfg, "version": 1}), http.StatusBadRequest)
}

func TestPublishRetryAfterPartial(t *testing.T) {
	app, mail := newTestApp(t)
	tok, _ := signIn(t, app, mail, "a@example.com")
	// No profile yet: publish says so.
	wantError(t, do(t, app, "PUT", "/team", tok, map[string]any{"name": "Reds", "config": cfg, "version": 1}),
		http.StatusBadRequest, "create a profile first")
	wantStatus(t, do(t, app, "POST", "/profile", tok, map[string]string{"username": "alice"}), http.StatusCreated)
	// The retry replays both calls: the profile is a 409, the team goes through.
	wantError(t, do(t, app, "POST", "/profile", tok, map[string]string{"username": "alice"}), http.StatusConflict, "profile already exists")
	publish(t, app, tok, "Reds")
}

func TestDeleteNeedsConfirm(t *testing.T) {
	app, mail := newTestApp(t)
	tok, _ := userWithProfile(t, app, mail, "a@example.com", "alice")
	wantStatus(t, do(t, app, "POST", "/account/delete", tok, map[string]string{}), http.StatusBadRequest)
	wantStatus(t, do(t, app, "POST", "/account/delete", tok, map[string]string{"confirm": "delete"}), http.StatusBadRequest)
	wantStatus(t, do(t, app, "GET", "/me", tok, nil), http.StatusOK) // still signed in
}

func TestDeleteCascades(t *testing.T) {
	app, mail := newTestApp(t)
	ctx := context.Background()
	tokA, ua := userWithProfile(t, app, mail, "a@example.com", "alice")
	tokB, ub := userWithProfile(t, app, mail, "b@example.com", "bobby")
	a, b := publish(t, app, tokA, "Alice FC"), publish(t, app, tokB, "Bob FC")
	if _, err := app.DB.Exec(ctx, `
		insert into matches (team_a, team_b, version_a, version_b, seed, score_a, score_b,
			elo_delta_a, elo_delta_b, engine_version, config_a, config_b)
		values ($1, $2, 1, 1, 1, 2, 1, 16, -16, 1, '{}', '{}')`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}

	rr := do(t, app, "POST", "/account/delete", tokA, map[string]string{"confirm": "DELETE"})
	wantStatus(t, rr, http.StatusOK)
	if got := decode[struct {
		Deleted bool
		UserID  string
	}](t, rr); !got.Deleted || got.UserID != ua.ID {
		t.Fatalf("got %+v", got)
	}

	count := func(q string, args ...any) int {
		var n int
		if err := app.DB.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("select count(*) from users where id = $1", ua.ID) +
		count("select count(*) from profiles where id = $1", ua.ID) +
		count("select count(*) from teams where id = $1", a.ID) +
		count("select count(*) from matches") +
		count("select count(*) from sessions where user_id = $1", ua.ID); n != 0 {
		t.Fatalf("%d of A's rows survived", n)
	}
	if n := count("select count(*) from teams where id = $1", b.ID) +
		count("select count(*) from profiles where id = $1", ub.ID); n != 2 {
		t.Fatal("B's rows were touched")
	}
	wantStatus(t, do(t, app, "GET", "/me", tokA, nil), http.StatusUnauthorized)
	wantStatus(t, do(t, app, "GET", "/me", tokB, nil), http.StatusOK)
}
