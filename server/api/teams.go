package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
)

// pgCode is the Postgres sqlstate and constraint name of err, if it has one.
func pgCode(err error) (code, constraint string) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code, pe.ConstraintName
	}
	return "", ""
}

func (a *App) handleCreateProfile(w http.ResponseWriter, r *http.Request, userID string) {
	var body struct{ Username string }
	if !readJSON(w, r, &body) {
		return
	}
	if existing, err := a.loadProfile(r.Context(), userID); err != nil {
		serverErr(w, err)
		return
	} else if existing != nil {
		writeErr(w, http.StatusConflict, "profile already exists")
		return
	}
	var p profileOut
	err := a.DB.QueryRow(r.Context(),
		"insert into profiles (id, username) values ($1, $2) returning id::text, username",
		userID, body.Username).Scan(&p.ID, &p.Username)
	switch code, constraint := pgCode(err); {
	case err == nil:
		writeJSON(w, http.StatusCreated, p)
	case code == "23505" && constraint == "profiles_pkey": // lost a race with ourselves
		writeErr(w, http.StatusConflict, "profile already exists")
	case code == "23505":
		writeErr(w, http.StatusConflict, "username taken")
	case code == "23514":
		writeErr(w, http.StatusBadRequest, "username must be 3-20 characters")
	default:
		serverErr(w, err)
	}
}

func (a *App) handlePublishTeam(w http.ResponseWriter, r *http.Request, userID string) {
	var body struct {
		Name    string
		Config  json.RawMessage
		Version int
	}
	if !readJSON(w, r, &body) {
		return
	}
	if len(body.Config) == 0 || body.Version < 1 {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Elo, wins, draws and losses are in neither the insert nor the update
	// list, so nothing in the body can touch them.
	var t teamOut
	err := a.DB.QueryRow(r.Context(), `
		insert into teams (owner, name, config, version) values ($1, $2, $3, $4)
		on conflict (owner) do update
		set name = excluded.name, config = excluded.config, version = excluded.version
		returning id::text, name, elo, version`,
		userID, body.Name, []byte(body.Config), body.Version).Scan(&t.ID, &t.Name, &t.Elo, &t.Version)
	switch code, constraint := pgCode(err); {
	case err == nil:
		writeJSON(w, http.StatusOK, t)
	case code == "23503": // owner has no profiles row
		writeErr(w, http.StatusBadRequest, "create a profile first")
	case code == "23514" && constraint == "teams_config_sane":
		writeErr(w, http.StatusBadRequest, "team config must be an object under 8 KB")
	case code == "23514":
		writeErr(w, http.StatusBadRequest, "team name must be 1-24 characters")
	default:
		serverErr(w, err)
	}
}

func (a *App) handleDeleteAccount(w http.ResponseWriter, r *http.Request, userID string) {
	var body struct{ Confirm string }
	if !readJSON(w, r, &body) {
		return
	}
	if body.Confirm != "DELETE" {
		writeErr(w, http.StatusBadRequest, `send {"confirm": "DELETE"} to delete your account`)
		return
	}
	// Cascades: sessions, profile, team, matches.
	if _, err := a.DB.Exec(r.Context(), "delete from users where id = $1", userID); err != nil {
		serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "userId": userID})
}
