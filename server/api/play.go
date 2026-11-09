package main

// POST /play: port of supabase/functions/play-match. The only writer of ranked
// results. Configs are validated and matches simulated by the sim service;
// Elo and the match row land in one transaction via apply_match_result, which
// also enforces the hourly cap under an advisory lock (sqlstate PT429).

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	matchesPerHour = 10 // per-team hourly cap, both modes (CHALLENGES_PER_HOUR)
	placementCount = 5
	capCode        = "PT429"
)

var (
	msgCapped     = fmt.Sprintf("rate limit: %d matches/hour", matchesPerHour)
	msgSimDown    = "match server unavailable"
	errCapped     = errors.New("capped")
	errSimFailed  = errors.New("sim failed")
	errNoSuchTeam = errors.New("no such team")
)

type playTeam struct {
	ID, Name, Owner string
	Config          json.RawMessage
	Version, Elo    int
}

type matchOut struct {
	Opponent   string `json:"opponent"`
	OpponentID string `json:"opponentId"`
	Seed       uint32 `json:"seed"`
	Score      [2]int `json:"score"`
	DeltaA     int    `json:"deltaA"`
}

const teamCols = `select id::text, name, owner::text, config, version, elo from teams`

func scanTeam(row pgx.Row) (playTeam, error) {
	var t playTeam
	err := row.Scan(&t.ID, &t.Name, &t.Owner, &t.Config, &t.Version, &t.Elo)
	return t, err
}

func (a *App) loadPlayTeam(ctx context.Context, id string) (playTeam, error) {
	t, err := scanTeam(a.DB.QueryRow(ctx, teamCols+` where id = $1`, id))
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == "22P02") {
		return t, errNoSuchTeam // missing, or not even a uuid
	}
	return t, err
}

// playOne simulates one match with a fresh server seed and persists it. It
// updates a.Elo / b.Elo so a placement run rates each match off live ratings.
func (a *App) playOne(ctx context.Context, me, opp *playTeam) (matchOut, error) {
	var buf [4]byte
	rand.Read(buf[:])
	seed := binary.LittleEndian.Uint32(buf[:])

	res, err := a.Sim.Play(ctx, SimTeam{me.Config, me.Elo}, SimTeam{opp.Config, opp.Elo}, seed)
	if err != nil {
		log.Printf("sim play: %v", err)
		return matchOut{}, errSimFailed
	}
	_, err = a.DB.Exec(ctx, `select apply_match_result(
		p_team_a => $1, p_team_b => $2, p_version_a => $3, p_version_b => $4,
		p_seed => $5, p_score_a => $6, p_score_b => $7,
		p_delta_a => $8, p_delta_b => $9, p_engine_version => $10,
		p_config_a => $11, p_config_b => $12, p_limit => $13)`,
		me.ID, opp.ID, me.Version, opp.Version,
		int64(seed), res.Score[0], res.Score[1],
		res.DeltaA, -res.DeltaA, res.EngineVersion,
		me.Config, opp.Config, matchesPerHour)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == capCode {
		return matchOut{}, errCapped
	}
	if err != nil {
		return matchOut{}, err
	}
	me.Elo += res.DeltaA
	opp.Elo -= res.DeltaA
	return matchOut{opp.Name, opp.ID, seed, res.Score, res.DeltaA}, nil
}

// playErr answers for an error from playOne or the sim.
func playErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errCapped):
		writeErr(w, http.StatusTooManyRequests, msgCapped)
	case errors.Is(err, errSimFailed):
		writeErr(w, http.StatusServiceUnavailable, msgSimDown)
	default:
		serverErr(w, err)
	}
}

func (a *App) validate(ctx context.Context, configs []json.RawMessage) ([]Validation, error) {
	v, err := a.Sim.Validate(ctx, configs)
	if err != nil {
		log.Printf("sim validate: %v", err)
		return nil, errSimFailed
	}
	return v, nil
}

func (a *App) handlePlay(w http.ResponseWriter, r *http.Request, userID string) {
	var body struct {
		Mode       string `json:"mode"`
		TeamID     string `json:"teamId"`
		OpponentID string `json:"opponentId"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.TeamID == "" || (body.Mode != "challenge" && body.Mode != "placement") {
		writeErr(w, http.StatusBadRequest, "mode must be challenge or placement, with teamId")
		return
	}
	ctx := r.Context()

	// The caller's team: must exist, be theirs, and validate (the builder is
	// not trusted). Ownership is checked against the session user only.
	me, err := a.loadPlayTeam(ctx, body.TeamID)
	if errors.Is(err, errNoSuchTeam) {
		writeErr(w, http.StatusNotFound, "team not found")
		return
	}
	if err != nil {
		serverErr(w, err)
		return
	}
	if me.Owner != userID {
		writeErr(w, http.StatusForbidden, "not your team")
		return
	}
	check, err := a.validate(ctx, []json.RawMessage{me.Config})
	if err != nil {
		playErr(w, err)
		return
	}
	if !check[0].OK {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid team config", "details": check[0].Errors})
		return
	}

	if body.Mode == "challenge" {
		if body.OpponentID == "" || body.OpponentID == body.TeamID {
			writeErr(w, http.StatusBadRequest, "challenge needs a different opponentId")
			return
		}
		opp, err := a.loadPlayTeam(ctx, body.OpponentID)
		if errors.Is(err, errNoSuchTeam) {
			writeErr(w, http.StatusNotFound, "opponent not found")
			return
		}
		if err != nil {
			serverErr(w, err)
			return
		}
		check, err := a.validate(ctx, []json.RawMessage{opp.Config})
		if err != nil {
			playErr(w, err)
			return
		}
		if !check[0].OK {
			writeErr(w, http.StatusUnprocessableEntity, "opponent config is invalid (stale schema?)")
			return
		}
		m, err := a.playOne(ctx, &me, &opp)
		if err != nil {
			playErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"mode": body.Mode, "matches": []matchOut{m}, "elo": me.Elo})
		return
	}

	// Placement: up to five matches vs the nearest-Elo valid teams. Small
	// ladder, so fetch all and sort here.
	rows, err := a.DB.Query(ctx, teamCols+` where id <> $1 order by created_at`, me.ID)
	if err != nil {
		serverErr(w, err)
		return
	}
	others, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (playTeam, error) { return scanTeam(row) })
	if err != nil {
		serverErr(w, err)
		return
	}
	var candidates []playTeam
	if len(others) > 0 {
		configs := make([]json.RawMessage, len(others))
		for i, t := range others {
			configs[i] = t.Config
		}
		checks, err := a.validate(ctx, configs)
		if err != nil {
			playErr(w, err)
			return
		}
		for i, t := range others {
			if checks[i].OK {
				candidates = append(candidates, t)
			}
		}
	}
	dist := func(t playTeam) int { d := t.Elo - me.Elo; return max(d, -d) }
	slices.SortStableFunc(candidates, func(x, y playTeam) int { return cmp.Compare(dist(x), dist(y)) })
	candidates = candidates[:min(len(candidates), placementCount)]
	if len(candidates) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"mode": body.Mode, "matches": []matchOut{}, "elo": me.Elo, "note": "no opponents yet"})
		return
	}

	// The cap is checked per match: the call that hits it stops, and a short
	// run is a normal 200 with however many actually happened.
	results := []matchOut{}
	for i := range candidates {
		m, err := a.playOne(ctx, &me, &candidates[i])
		if errors.Is(err, errCapped) {
			break
		}
		if err != nil {
			playErr(w, err)
			return
		}
		results = append(results, m)
	}
	if len(results) == 0 {
		writeErr(w, http.StatusTooManyRequests, msgCapped)
		return
	}
	out := map[string]any{"mode": body.Mode, "matches": results, "elo": me.Elo}
	if len(results) < len(candidates) {
		out["note"] = fmt.Sprintf("stopped at the %d/hour cap", matchesPerHour)
	}
	writeJSON(w, http.StatusOK, out)
}
