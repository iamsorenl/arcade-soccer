package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// The sim service (server/sim/sim.mjs) runs the game's own validator, engine
// and Elo math. The API never simulates anything itself.

type Validation struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors"`
}

type SimTeam struct {
	Config json.RawMessage `json:"config"`
	Elo    int             `json:"elo"`
}

type SimResult struct {
	Score         [2]int `json:"score"`
	DeltaA        int    `json:"deltaA"`
	EngineVersion int    `json:"engineVersion"`
}

type Sim interface {
	Validate(ctx context.Context, configs []json.RawMessage) ([]Validation, error)
	Play(ctx context.Context, a, b SimTeam, seed uint32) (SimResult, error)
}

type httpSim struct {
	base   string
	client *http.Client
}

func newHTTPSim(base string) httpSim {
	return httpSim{base: base, client: &http.Client{Timeout: 5 * time.Second}}
}

func (s httpSim) post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("sim %s: status %d", path, res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (s httpSim) Validate(ctx context.Context, configs []json.RawMessage) ([]Validation, error) {
	var out struct{ Results []Validation }
	if err := s.post(ctx, "/validate", map[string]any{"configs": configs}, &out); err != nil {
		return nil, err
	}
	if len(out.Results) != len(configs) {
		return nil, fmt.Errorf("sim /validate: %d results for %d configs", len(out.Results), len(configs))
	}
	return out.Results, nil
}

func (s httpSim) Play(ctx context.Context, a, b SimTeam, seed uint32) (SimResult, error) {
	var out SimResult
	err := s.post(ctx, "/play", map[string]any{"a": a, "b": b, "seed": seed}, &out)
	return out, err
}
