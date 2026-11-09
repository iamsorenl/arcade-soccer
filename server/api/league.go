package main

import (
	"net/http"
	"regexp"
	"strconv"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// limitParam reads ?limit=, falling back to def when missing or not a number,
// and clamps to 1..200.
func limitParam(r *http.Request, def int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		n = def
	}
	return min(max(n, 1), 200)
}

// writeRawJSON sends a JSON document Postgres already built.
func writeRawJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// handleLeaderboard: top teams by Elo, in the shape league.js reads
// (username nested under "profiles", no owner).
func (a *App) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	var body []byte
	err := a.DB.QueryRow(r.Context(), `
		select coalesce(json_agg(json_build_object(
			'id', id, 'name', name, 'elo', elo, 'wins', wins, 'draws', draws,
			'losses', losses, 'config', config,
			'profiles', json_build_object('username', username)
		) order by elo desc, created_at), '[]')::text
		from (
			select t.*, p.username from teams t join profiles p on p.id = t.owner
			order by t.elo desc, t.created_at limit $1
		) s`, limitParam(r, 50)).Scan(&body)
	if err != nil {
		serverErr(w, err)
		return
	}
	writeRawJSON(w, body)
}

// handleMatches: newest matches first, optionally only those involving ?team=.
func (a *App) handleMatches(w http.ResponseWriter, r *http.Request) {
	var team any // nil = no filter
	if s := r.URL.Query().Get("team"); s != "" {
		if !uuidRe.MatchString(s) {
			writeErr(w, http.StatusBadRequest, "invalid team id")
			return
		}
		team = s
	}
	var body []byte
	err := a.DB.QueryRow(r.Context(), `
		select coalesce(json_agg(json_build_object(
			'id', id, 'team_a', team_a, 'team_b', team_b,
			'score_a', score_a, 'score_b', score_b,
			'elo_delta_a', elo_delta_a, 'elo_delta_b', elo_delta_b,
			'seed', seed, 'engine_version', engine_version,
			'config_a', config_a, 'config_b', config_b, 'created_at', created_at
		) order by created_at desc), '[]')::text
		from (
			select * from matches
			where $1::uuid is null or team_a = $1::uuid or team_b = $1::uuid
			order by created_at desc limit $2
		) s`, team, limitParam(r, 100)).Scan(&body)
	if err != nil {
		serverErr(w, err)
		return
	}
	writeRawJSON(w, body)
}
