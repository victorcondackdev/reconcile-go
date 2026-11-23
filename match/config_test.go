package match

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	cfg, err := LoadConfig(strings.NewReader(`{
		"passes": {"fuzzy": false},
		"exact": {"date_window_days": 1},
		"group": {"time_budget": "250ms"},
		"fuzzy": {"weights": {"amount": 1, "date": 0, "counterparty": 0, "description": 0}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Passes.Fuzzy || !cfg.Passes.Reference || cfg.Exact.DateWindowDays != 1 ||
		time.Duration(cfg.Group.TimeBudget) != 250*time.Millisecond || cfg.Fuzzy.Weights.Amount != 1 {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if len(cfg.Group.Rules) != 1 || cfg.Group.Rules[0].Name != "combined-payment" {
		t.Errorf("default rules lost: %+v", cfg.Group.Rules)
	}
	cfg, err = LoadConfig(strings.NewReader(`{"group": {"rules": [], "time_budget": 1000}}`))
	if err != nil || len(cfg.Group.Rules) != 0 || cfg.Group.TimeBudget != 1000 {
		t.Errorf("explicit empty rules / numeric duration: %+v %v", cfg.Group, err)
	}
	for _, bad := range []string{
		`{"nope": 1}`, `{"group": {"time_budget": "soon"}}`, `{"group": {"time_budget": true}}`,
		`{"group": {"max_group_size": 1}}`, `not json`,
	} {
		if _, err := LoadConfig(strings.NewReader(bad)); err == nil {
			t.Errorf("LoadConfig(%s) accepted", bad)
		}
	}
	b, err := json.Marshal(DefaultConfig())
	if err != nil || !strings.Contains(string(b), `"time_budget":"5s"`) {
		t.Errorf("marshal: %s %v", b, err)
	}
	if _, err := LoadConfig(strings.NewReader(string(b))); err != nil {
		t.Errorf("round trip: %v", err)
	}
}

func TestValidate(t *testing.T) {
	mut := []func(*Config){
		func(c *Config) { c.Reference.MinTokenLength = 0 },
		func(c *Config) { c.Exact.DateWindowDays = -1 },
		func(c *Config) { c.Exact.MinCounterpartySimilarity = 1.5 },
		func(c *Config) { c.Fuzzy.MinScore = -0.1 },
		func(c *Config) { c.Group.MaxGroupSize = 65 },
		func(c *Config) { c.Group.MaxCandidates = 1 },
		func(c *Config) { c.Group.MaxNodes = 0 },
		func(c *Config) { c.Group.TimeBudget = -1 },
		func(c *Config) { c.Group.Split.ToleranceCents = -1 },
		func(c *Config) { c.Group.Rules = []GroupRule{{Name: "x", FeeMinPercent: 3, FeeMaxPercent: 2}} },
		func(c *Config) { c.Group.Rules = []GroupRule{{Name: "x", FeeMaxPercent: 100}} },
		func(c *Config) { c.Group.Rules = []GroupRule{{Name: "x", FeeFixedCents: -1}} },
		func(c *Config) { c.Fuzzy.AmountTolerancePercent = 100 },
		func(c *Config) { c.Fuzzy.Weights = Weights{} },
		func(c *Config) { c.Fuzzy.Weights.Date = -1 },
		func(c *Config) { c.Assignment.HungarianMaxSize = 5001 },
	}
	for i, m := range mut {
		c := DefaultConfig()
		m(&c)
		if err := c.Validate(); !errors.Is(err, ErrConfig) {
			t.Errorf("mutation %d: %v", i, err)
		}
	}
	if err := DefaultConfig().Validate(); err != nil {
		t.Errorf("default config invalid: %v", err)
	}
}
