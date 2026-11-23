package match

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"
)

// Duration is a time.Duration that encodes to JSON as a string ("250ms").
type Duration time.Duration

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON accepts a duration string ("2s") or a number of
// nanoseconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = Duration(v)
		return nil
	}
	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return fmt.Errorf("duration must be a string like \"250ms\" or integer nanoseconds")
	}
	*d = Duration(n)
	return nil
}

// Config controls the matching passes. Start from DefaultConfig and
// override what you need; LoadConfig applies a JSON document on top of the
// defaults.
type Config struct {
	Passes     Passes           `json:"passes"`
	Reference  ReferenceConfig  `json:"reference"`
	Exact      ExactConfig      `json:"exact"`
	Group      GroupConfig      `json:"group"`
	Fuzzy      FuzzyConfig      `json:"fuzzy"`
	Assignment AssignmentConfig `json:"assignment"`
	Duplicates DuplicateConfig  `json:"duplicates"`
}

// Passes enables or disables each pass. Passes always run in this order.
type Passes struct {
	Reference bool `json:"reference"`
	Exact     bool `json:"exact"`
	Group     bool `json:"group"`
	Fuzzy     bool `json:"fuzzy"`
}

// ReferenceConfig controls pass 1, exact identifier matching.
type ReferenceConfig struct {
	// SearchDescription also looks for expected references among the
	// tokens of the statement description (banks often print the boleto
	// "nosso número" or the order number there).
	SearchDescription bool `json:"search_description"`
	// MinTokenLength is the minimum length of a normalized identifier
	// taken from a description, to avoid matching short numbers.
	MinTokenLength int `json:"min_token_length"`
	// MatchExpectedID also compares statement identifiers with the
	// expected record ID, not only with its Reference.
	MatchExpectedID bool `json:"match_expected_id"`
	// MaxDateDiffDays rejects reference matches whose dates are further
	// apart than this. Zero disables the check.
	MaxDateDiffDays int `json:"max_date_diff_days"`
}

// ExactConfig controls pass 2: same amount, date window, counterparty.
type ExactConfig struct {
	// DateWindowDays is the maximum distance between booking and expected
	// date, in calendar days.
	DateWindowDays int `json:"date_window_days"`
	// MinCounterpartySimilarity rejects pairs whose party names are both
	// known and less similar than this (0 to 1).
	MinCounterpartySimilarity float64 `json:"min_counterparty_similarity"`
}

// GroupConfig controls pass 3, many-to-one and one-to-many matching by
// bounded subset-sum search.
type GroupConfig struct {
	// MaxGroupSize bounds the number of records combined into one match.
	MaxGroupSize int `json:"max_group_size"`
	// MaxCandidates bounds the records considered for one target, after
	// ranking by rule fit, party similarity and date proximity.
	MaxCandidates int `json:"max_candidates"`
	// MaxNodes bounds the search tree explored per target. It is the
	// deterministic budget: results never depend on machine speed unless
	// TimeBudget is hit first.
	MaxNodes int `json:"max_nodes"`
	// TimeBudget caps wall-clock time for the whole pass; a safety net for
	// adversarial inputs. Zero disables it.
	TimeBudget Duration `json:"time_budget"`
	// LookbackDays and LookaheadDays bound expected dates relative to the
	// statement entry for many-to-one groups (settlements come after
	// sales).
	LookbackDays  int `json:"lookback_days"`
	LookaheadDays int `json:"lookahead_days"`
	// Rules describe which entries may settle groups of expected records
	// and with which fees. The first rule that accepts a group wins.
	Rules []GroupRule `json:"rules"`
	// Split enables one-to-many matching: one expected record paid by
	// several statement entries from the same party.
	Split SplitConfig `json:"split"`
}

// GroupRule describes one kind of grouped settlement, for example an
// acquirer paying a day of card sales net of its merchant discount rate.
type GroupRule struct {
	Name string `json:"name"`
	// Match, if set, must occur (accent and case-insensitive) in the
	// entry's description or counterparty.
	Match string `json:"match,omitempty"`
	// Tag, if set, must be among the expected record's tags.
	Tag string `json:"tag,omitempty"`
	// FeeMinPercent and FeeMaxPercent bound the fee withheld, as a
	// percentage of the gross sum: gross - net must lie in
	// [gross*min%, gross*max%] plus FeeFixedCents per record, give or take
	// ToleranceCents per record for rounding.
	FeeMinPercent  float64 `json:"fee_min_percent"`
	FeeMaxPercent  float64 `json:"fee_max_percent"`
	FeeFixedCents  int64   `json:"fee_fixed_cents"`
	ToleranceCents int64   `json:"tolerance_cents"`
	// SameDate restricts a group to records sharing one expected date, as
	// acquirers settle each day of sales separately. Each date is searched
	// on its own, which also keeps the search small.
	SameDate bool `json:"same_date,omitempty"`
	// MinLagDays is the minimum number of days between an expected record
	// and the entry settling it (1 for D+1 settlement).
	MinLagDays int `json:"min_lag_days,omitempty"`
	// RequireCounterparty demands that every grouped record's party is
	// similar (Exact.MinCounterpartySimilarity) to the entry's text.
	RequireCounterparty bool `json:"require_counterparty"`
}

// SplitConfig controls one-to-many matching.
type SplitConfig struct {
	Enabled bool `json:"enabled"`
	// WindowDays bounds entry dates from the expected date.
	WindowDays int `json:"window_days"`
	// MinCounterpartySimilarity is required between each entry and the
	// expected record's party.
	MinCounterpartySimilarity float64 `json:"min_counterparty_similarity"`
	ToleranceCents            int64   `json:"tolerance_cents"`
}

// FuzzyConfig controls pass 4, scored matching with tolerances.
type FuzzyConfig struct {
	// AmountToleranceCents and AmountTolerancePercent bound the amount
	// difference; the larger of the two applies.
	AmountToleranceCents   int64   `json:"amount_tolerance_cents"`
	AmountTolerancePercent float64 `json:"amount_tolerance_percent"`
	DateToleranceDays      int     `json:"date_tolerance_days"`
	// MinCounterpartySimilarity rejects pairs whose party names are both
	// known and less similar than this: two different people paying the
	// same amount on nearby days are not evidence of a match.
	MinCounterpartySimilarity float64 `json:"min_counterparty_similarity"`
	// MinScore is the weighted score (0 to 1) below which a pair is not a
	// candidate.
	MinScore float64 `json:"min_score"`
	Weights  Weights `json:"weights"`
}

// Weights weight the fuzzy features. They need not sum to 1.
type Weights struct {
	Amount       float64 `json:"amount"`
	Date         float64 `json:"date"`
	Counterparty float64 `json:"counterparty"`
	Description  float64 `json:"description"`
}

// AssignmentConfig controls how competing one-to-one candidates are
// resolved.
type AssignmentConfig struct {
	// HungarianMaxSize is the largest side of a candidate component solved
	// optimally with the Hungarian algorithm (O(n^3)); larger components
	// fall back to greedy best-score-first assignment.
	HungarianMaxSize int `json:"hungarian_max_size"`
}

// DuplicateConfig controls duplicate detection.
type DuplicateConfig struct {
	Enabled bool `json:"enabled"`
}

// DefaultConfig returns the configuration used when none is given. It is
// tuned for Brazilian receivables: Pix, boletos, TED and card acquirer
// settlements.
func DefaultConfig() Config {
	return Config{
		Passes: Passes{Reference: true, Exact: true, Group: true, Fuzzy: true},
		Reference: ReferenceConfig{
			SearchDescription: true, MinTokenLength: 6, MatchExpectedID: true, MaxDateDiffDays: 60,
		},
		Exact: ExactConfig{DateWindowDays: 3, MinCounterpartySimilarity: 0.75},
		Group: GroupConfig{
			MaxGroupSize: 8, MaxCandidates: 32, MaxNodes: 200_000,
			TimeBudget:   Duration(5 * time.Second),
			LookbackDays: 4, LookaheadDays: 0,
			Rules: []GroupRule{
				{Name: "combined-payment", ToleranceCents: 0, RequireCounterparty: true},
			},
			Split: SplitConfig{Enabled: true, WindowDays: 5, MinCounterpartySimilarity: 0.85},
		},
		Fuzzy: FuzzyConfig{
			AmountToleranceCents: 500, AmountTolerancePercent: 3, DateToleranceDays: 5,
			MinCounterpartySimilarity: 0.7, MinScore: 0.62,
			Weights: Weights{Amount: 0.35, Date: 0.2, Counterparty: 0.3, Description: 0.15},
		},
		Assignment: AssignmentConfig{HungarianMaxSize: 400},
		Duplicates: DuplicateConfig{Enabled: true},
	}
}

// LoadConfig decodes a JSON configuration on top of DefaultConfig. Fields
// absent from the document keep their default values; "group.rules", when
// present, replaces the default rules. Unknown fields are rejected.
func LoadConfig(r io.Reader) (Config, error) {
	cfg := DefaultConfig()
	defRules := cfg.Group.Rules
	cfg.Group.Rules = nil
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("match: config: %w", err)
	}
	if cfg.Group.Rules == nil {
		cfg.Group.Rules = defRules
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ErrConfig is wrapped by validation errors.
var ErrConfig = errors.New("match: invalid config")

// Validate checks ranges and consistency.
func (c Config) Validate() error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrConfig, fmt.Sprintf(format, args...))
	}
	unit := func(name string, v float64) error {
		if v < 0 || v > 1 {
			return bad("%s must be in [0,1], got %v", name, v)
		}
		return nil
	}
	if c.Reference.MinTokenLength < 1 {
		return bad("reference.min_token_length must be >= 1")
	}
	if c.Reference.MaxDateDiffDays < 0 || c.Exact.DateWindowDays < 0 || c.Fuzzy.DateToleranceDays < 0 ||
		c.Group.LookbackDays < 0 || c.Group.LookaheadDays < 0 || c.Group.Split.WindowDays < 0 {
		return bad("day windows must be >= 0")
	}
	for name, v := range map[string]float64{
		"exact.min_counterparty_similarity":       c.Exact.MinCounterpartySimilarity,
		"group.split.min_counterparty_similarity": c.Group.Split.MinCounterpartySimilarity,
		"fuzzy.min_score":                         c.Fuzzy.MinScore,
		"fuzzy.min_counterparty_similarity":       c.Fuzzy.MinCounterpartySimilarity,
	} {
		if err := unit(name, v); err != nil {
			return err
		}
	}
	if c.Group.MaxGroupSize < 2 || c.Group.MaxGroupSize > 64 {
		return bad("group.max_group_size must be in [2,64]")
	}
	if c.Group.MaxCandidates < 2 || c.Group.MaxCandidates > 256 {
		return bad("group.max_candidates must be in [2,256]")
	}
	if c.Group.MaxNodes < 1 {
		return bad("group.max_nodes must be >= 1")
	}
	if c.Group.TimeBudget < 0 {
		return bad("group.time_budget must be >= 0")
	}
	if c.Group.Split.ToleranceCents < 0 {
		return bad("group.split.tolerance_cents must be >= 0")
	}
	for i, r := range c.Group.Rules {
		if r.FeeMinPercent < 0 || r.FeeMaxPercent < r.FeeMinPercent || r.FeeMaxPercent >= 100 {
			return bad("group.rules[%d] (%s): need 0 <= fee_min_percent <= fee_max_percent < 100", i, r.Name)
		}
		if r.FeeFixedCents < 0 || r.ToleranceCents < 0 || r.MinLagDays < 0 {
			return bad("group.rules[%d] (%s): fee_fixed_cents, tolerance_cents and min_lag_days must be >= 0", i, r.Name)
		}
	}
	f := c.Fuzzy
	if f.AmountToleranceCents < 0 || f.AmountTolerancePercent < 0 || f.AmountTolerancePercent >= 100 {
		return bad("fuzzy amount tolerances must be >= 0 and percent < 100")
	}
	w := f.Weights
	if w.Amount < 0 || w.Date < 0 || w.Counterparty < 0 || w.Description < 0 || w.Amount+w.Date+w.Counterparty+w.Description == 0 {
		return bad("fuzzy.weights must be >= 0 with a positive sum")
	}
	if c.Assignment.HungarianMaxSize < 0 || c.Assignment.HungarianMaxSize > 5000 {
		return bad("assignment.hungarian_max_size must be in [0,5000]")
	}
	return nil
}
