package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type playResp struct {
	Mode    string
	Matches []struct {
		Opponent   string
		OpponentID string
		Seed       uint32
		Score      [2]int
		DeltaA     int
	}
	Elo  int
	Note string
}

func newPlayApp(t *testing.T, sim *fakeSim) *App {
	t.Helper()
	app, _ := newTestApp(t)
	app.Sim = sim
	return app
}

func challenge(t *testing.T, app *App, session, teamID, oppID string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, app, "POST", "/play", session, map[string]string{"mode": "challenge", "teamId": teamID, "opponentId": oppID})
}

func TestChallengeWritesMatch(t *testing.T) {
	app := newPlayApp(t, &fakeSim{result: SimResult{Score: [2]int{2, 1}, DeltaA: 16, EngineVersion: 3}})
	sa, a := seedTeam(t, app, "alice", 1000)
	_, b := seedTeam(t, app, "bobby", 1000)

	rr := challenge(t, app, sa, a, b)
	wantStatus(t, rr, http.StatusOK)
	res := decode[playResp](t, rr)
	if res.Mode != "challenge" || res.Elo != 1016 || len(res.Matches) != 1 {
		t.Fatalf("response %+v", res)
	}
	m := res.Matches[0]
	if m.OpponentID != b || m.Opponent != "bobby FC" || m.Score != [2]int{2, 1} || m.DeltaA != 16 {
		t.Fatalf("match %+v", m)
	}
	if n := countRows(t, app, `select count(*) from matches where team_a = $1 and team_b = $2 and seed = $3
		and score_a = 2 and score_b = 1 and elo_delta_a = 16 and elo_delta_b = -16 and engine_version = 3`,
		a, b, int64(m.Seed)); n != 1 {
		t.Fatalf("%d matching rows, want 1", n)
	}
	if n := countRows(t, app, `select elo from teams where id = $1`, a); n != 1016 {
		t.Fatalf("A elo %d", n)
	}
	if n := countRows(t, app, `select elo from teams where id = $1`, b); n != 984 {
		t.Fatalf("B elo %d", n)
	}
}

func TestChallengeNotYourTeam(t *testing.T) {
	app := newPlayApp(t, &fakeSim{})
	sa, _ := seedTeam(t, app, "alice", 1000)
	_, b := seedTeam(t, app, "bobby", 1000)
	_, c := seedTeam(t, app, "carol", 1000)
	wantError(t, challenge(t, app, sa, b, c), http.StatusForbidden, "not your team")
	wantError(t, challenge(t, app, sa, "00000000-0000-0000-0000-000000000000", c), http.StatusNotFound, "team not found")
	wantError(t, challenge(t, app, sa, "not-a-uuid", c), http.StatusNotFound, "team not found")
}

func TestChallengeSelf(t *testing.T) {
	app := newPlayApp(t, &fakeSim{})
	sa, a := seedTeam(t, app, "alice", 1000)
	wantError(t, challenge(t, app, sa, a, a), http.StatusBadRequest, "challenge needs a different opponentId")
	wantError(t, challenge(t, app, sa, a, ""), http.StatusBadRequest, "challenge needs a different opponentId")
}

func TestPlacementFive(t *testing.T) {
	app := newPlayApp(t, &fakeSim{result: SimResult{Score: [2]int{1, 1}, DeltaA: 0, EngineVersion: 3}})
	sa, a := seedTeam(t, app, "alice", 1000)
	near := map[string]bool{}
	for i, elo := range []int{1500, 1010, 980, 1300, 1030, 960, 1100} {
		_, id := seedTeam(t, app, fmt.Sprintf("opp%d", i), elo)
		if elo > 900 && elo <= 1100 { // the five nearest to 1000
			near[id] = true
		}
	}

	rr := do(t, app, "POST", "/play", sa, map[string]string{"mode": "placement", "teamId": a})
	wantStatus(t, rr, http.StatusOK)
	res := decode[playResp](t, rr)
	if len(res.Matches) != 5 || res.Note != "" {
		t.Fatalf("response %+v", res)
	}
	for _, m := range res.Matches {
		if !near[m.OpponentID] {
			t.Fatalf("played %s, not one of the nearest five", m.Opponent)
		}
	}
	if n := countRows(t, app, `select count(*) from matches where team_a = $1`, a); n != 5 {
		t.Fatalf("%d match rows, want 5", n)
	}
}

func TestPlacementStopsAtCap(t *testing.T) {
	app := newPlayApp(t, &fakeSim{result: SimResult{Score: [2]int{1, 0}, DeltaA: 10, EngineVersion: 3}})
	sa, a := seedTeam(t, app, "alice", 1000)
	_, b := seedTeam(t, app, "bobby", 1000)
	for i := range 6 {
		seedTeam(t, app, fmt.Sprintf("opp%d", i), 1000+i)
	}
	if _, err := app.DB.Exec(context.Background(), `
		insert into matches (team_a, team_b, version_a, version_b, seed, score_a, score_b,
			elo_delta_a, elo_delta_b, engine_version, config_a, config_b)
		select $1, $2, 1, 1, g, 0, 0, 0, 0, 3, '{}', '{}' from generate_series(1, 8) g`, a, b); err != nil {
		t.Fatal(err)
	}

	rr := do(t, app, "POST", "/play", sa, map[string]string{"mode": "placement", "teamId": a})
	wantStatus(t, rr, http.StatusOK)
	res := decode[playResp](t, rr)
	if len(res.Matches) != 2 || res.Note != "stopped at the 10/hour cap" || res.Elo != 1020 {
		t.Fatalf("response %+v", res)
	}
	// Fully capped now: a second run plays nothing and is a 429.
	wantError(t, do(t, app, "POST", "/play", sa, map[string]string{"mode": "placement", "teamId": a}),
		http.StatusTooManyRequests, "rate limit: 10 matches/hour")
}

func TestCapUnderParallel(t *testing.T) {
	app := newPlayApp(t, &fakeSim{result: SimResult{Score: [2]int{0, 0}, DeltaA: 0, EngineVersion: 3}})
	sa, a := seedTeam(t, app, "alice", 1000)
	_, b := seedTeam(t, app, "bobby", 1000)

	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for range 20 {
		wg.Go(func() {
			rr := challenge(t, app, sa, a, b)
			mu.Lock()
			codes[rr.Code]++
			mu.Unlock()
		})
	}
	wg.Wait()
	if codes[http.StatusOK] != 10 || codes[http.StatusTooManyRequests] != 10 {
		t.Fatalf("status counts %v, want 10x200 and 10x429", codes)
	}
	if n := countRows(t, app, `select count(*) from matches`); n != 10 {
		t.Fatalf("%d match rows, want 10", n)
	}
}

func TestPlaySimDown(t *testing.T) {
	app := newPlayApp(t, &fakeSim{err: errors.New("connection refused")})
	sa, a := seedTeam(t, app, "alice", 1000)
	_, b := seedTeam(t, app, "bobby", 1000)
	wantError(t, challenge(t, app, sa, a, b), http.StatusServiceUnavailable, "match server unavailable")
	wantError(t, do(t, app, "POST", "/play", sa, map[string]string{"mode": "placement", "teamId": a}),
		http.StatusServiceUnavailable, "match server unavailable")
	if n := countRows(t, app, `select count(*) from matches`); n != 0 {
		t.Fatalf("%d match rows, want 0", n)
	}
	if n := countRows(t, app, `select count(*) from teams where elo <> 1000`); n != 0 {
		t.Fatal("elo changed with the sim down")
	}
}
