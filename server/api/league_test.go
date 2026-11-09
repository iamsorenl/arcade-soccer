package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// seedLeagueTeam inserts a user, profile and team directly, returning the team id.
func seedLeagueTeam(t *testing.T, app *App, name string, elo int) string {
	t.Helper()
	ctx := context.Background()
	var uid, tid string
	if err := app.DB.QueryRow(ctx, `insert into users (email) values ($1) returning id`, name+"@x.test").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB.Exec(ctx, `insert into profiles (id, username) values ($1, $2)`, uid, "user_"+name); err != nil {
		t.Fatal(err)
	}
	if err := app.DB.QueryRow(ctx,
		`insert into teams (owner, name, config, elo) values ($1, $2, '{"name":"x"}', $3) returning id`,
		uid, name, elo).Scan(&tid); err != nil {
		t.Fatal(err)
	}
	return tid
}

// seedMatch inserts a finished match directly, ago in the past.
func seedMatch(t *testing.T, app *App, a, b string, ago time.Duration) {
	t.Helper()
	_, err := app.DB.Exec(context.Background(), `
		insert into matches (team_a, team_b, version_a, version_b, seed, score_a, score_b,
			elo_delta_a, elo_delta_b, engine_version, config_a, config_b, created_at)
		values ($1, $2, 1, 1, 42, 2, 1, 8, -8, 1, '{"name":"a"}', '{"name":"b"}', now() - $3::interval)`,
		a, b, fmt.Sprintf("%d seconds", int(ago.Seconds())))
	if err != nil {
		t.Fatal(err)
	}
}

func TestLeaderboardOrderAndShape(t *testing.T) {
	app, _ := newTestApp(t)
	seedLeagueTeam(t, app, "low", 900)
	seedLeagueTeam(t, app, "high", 1200)
	seedLeagueTeam(t, app, "mid", 1000)

	rr := do(t, app, "GET", "/leaderboard", "", nil)
	wantStatus(t, rr, 200)
	rows := decode[[]struct {
		ID       string
		Name     string
		Elo      int
		Config   map[string]any
		Profiles struct{ Username string }
	}](t, rr)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	for i, want := range []string{"high", "mid", "low"} {
		if rows[i].Name != want {
			t.Fatalf("row %d is %q, want %q", i, rows[i].Name, want)
		}
	}
	if rows[0].Profiles.Username != "user_high" || rows[0].ID == "" || rows[0].Config["name"] != "x" {
		t.Fatalf("bad row shape: %+v", rows[0])
	}
	raw := decode[[]map[string]any](t, rr)
	if _, ok := raw[0]["owner"]; ok {
		t.Fatal("leaderboard leaks owner")
	}
}

func TestLeaderboardEmpty(t *testing.T) {
	app, _ := newTestApp(t)
	rr := do(t, app, "GET", "/leaderboard", "", nil)
	wantStatus(t, rr, 200)
	if got := rr.Body.String(); got != "[]" {
		t.Fatalf("body %q, want []", got)
	}
}

func TestMatchesFilter(t *testing.T) {
	app, _ := newTestApp(t)
	a, b := seedLeagueTeam(t, app, "a", 1000), seedLeagueTeam(t, app, "b", 1000)
	c, d := seedLeagueTeam(t, app, "c", 1000), seedLeagueTeam(t, app, "d", 1000)
	seedMatch(t, app, a, b, time.Minute)
	seedMatch(t, app, c, d, time.Second)

	type m struct {
		TeamA     string `json:"team_a"`
		ScoreA    int    `json:"score_a"`
		Seed      int64  `json:"seed"`
		CreatedAt string `json:"created_at"`
	}
	rr := do(t, app, "GET", "/matches?team="+b, "", nil)
	wantStatus(t, rr, 200)
	got := decode[[]m](t, rr)
	if len(got) != 1 || got[0].TeamA != a || got[0].ScoreA != 2 || got[0].Seed != 42 || got[0].CreatedAt == "" {
		t.Fatalf("filtered: %+v", got)
	}

	all := decode[[]m](t, do(t, app, "GET", "/matches", "", nil))
	if len(all) != 2 || all[0].TeamA != c {
		t.Fatalf("unfiltered should be 2, newest first (c): %+v", all)
	}
}

func TestMatchesBadTeam(t *testing.T) {
	app, _ := newTestApp(t)
	wantError(t, do(t, app, "GET", "/matches?team=nope", "", nil), 400, "invalid team id")
}

func TestLimitClamp(t *testing.T) {
	app, _ := newTestApp(t)
	a, b := seedLeagueTeam(t, app, "a", 1000), seedLeagueTeam(t, app, "b", 1000)
	if _, err := app.DB.Exec(context.Background(), `
		insert into matches (team_a, team_b, version_a, version_b, seed, score_a, score_b,
			elo_delta_a, elo_delta_b, engine_version, config_a, config_b)
		select $1, $2, 1, 1, g, 1, 0, 0, 0, 1, '{}', '{}' from generate_series(1, 250) g`, a, b); err != nil {
		t.Fatal(err)
	}
	if n := len(decode[[]any](t, do(t, app, "GET", "/matches?limit=100000", "", nil))); n != 200 {
		t.Fatalf("limit=100000 returned %d, want 200", n)
	}
	if n := len(decode[[]any](t, do(t, app, "GET", "/matches?limit=0", "", nil))); n != 1 {
		t.Fatalf("limit=0 returned %d, want 1", n)
	}
	if n := len(decode[[]any](t, do(t, app, "GET", "/matches?limit=abc", "", nil))); n != 100 {
		t.Fatalf("bad limit returned %d, want default 100", n)
	}
	wantStatus(t, do(t, app, "GET", "/leaderboard?limit=100000", "", nil), 200)
}
