package match

import (
	"time"

	"github.com/victorcondackdev/reconcile-go/statement"
)

// Expected is a record the business expects to see on the statement: an
// order, invoice, boleto, payout or supplier payment. AmountCents follows
// the statement sign convention: positive for money coming in, negative
// for money going out.
type Expected struct {
	ID          string    `json:"id"`
	AmountCents int64     `json:"amount_cents"`
	Date        time.Time `json:"date"`
	// Reference is the identifier the bank is expected to echo: Pix
	// EndToEndId, boleto nosso número, ISO 20022 EndToEndId, FITID.
	Reference    string `json:"reference,omitempty"`
	Counterparty string `json:"counterparty,omitempty"`
	Description  string `json:"description,omitempty"`
	// Tags select group rules (for example "card" or the acquirer name).
	Tags []string `json:"tags,omitempty"`
}

// Pass names a matching pass.
type Pass string

// The passes, in execution order.
const (
	PassReference Pass = "reference"
	PassExact     Pass = "exact"
	PassGroup     Pass = "group"
	PassFuzzy     Pass = "fuzzy"
)

// Kind describes the cardinality of a match.
type Kind string

// Match cardinalities.
const (
	OneToOne Kind = "one_to_one"
	// ManyToOne: several expected records settled by one statement entry
	// (acquirer payout, a customer paying several orders at once).
	ManyToOne Kind = "many_to_one"
	// OneToMany: one expected record paid by several statement entries
	// (split payment).
	OneToMany Kind = "one_to_many"
)

// Feature is one piece of evidence behind a match.
type Feature struct {
	Name string `json:"name"`
	// Detail explains the value in words ("+1 day", "0.93 'MARIA S'").
	Detail string `json:"detail"`
	// Score is the feature's similarity in [0, 1].
	Score float64 `json:"score"`
	// Weight is the feature's weight in the pass score (0 when the feature
	// is a hard constraint rather than a weighted term).
	Weight float64 `json:"weight"`
}

// Explanation says why a match was made.
type Explanation struct {
	// Rule is a one-line, human-readable reason.
	Rule     string    `json:"rule"`
	Features []Feature `json:"features"`
}

// Match links statement entries to expected records.
type Match struct {
	ID          string   `json:"id"`
	Pass        Pass     `json:"pass"`
	Kind        Kind     `json:"kind"`
	EntryIDs    []string `json:"entry_ids"`
	ExpectedIDs []string `json:"expected_ids"`
	// EntryCents and ExpectedCents are the totals on each side;
	// DiffCents = EntryCents - ExpectedCents (negative when fees were
	// withheld from a credit).
	EntryCents    int64       `json:"entry_cents"`
	ExpectedCents int64       `json:"expected_cents"`
	DiffCents     int64       `json:"diff_cents"`
	Confidence    float64     `json:"confidence"`
	Explanation   Explanation `json:"explanation"`

	entries  []int
	expected []int
}

// DuplicateGroup lists records suspected to be duplicates of each other.
type DuplicateGroup struct {
	// Side is "statement" or "expected".
	Side   string   `json:"side"`
	IDs    []string `json:"ids"`
	Reason string   `json:"reason"`
}

// Summary aggregates a Result.
type Summary struct {
	Entries                int          `json:"entries"`
	Expected               int          `json:"expected"`
	Matches                int          `json:"matches"`
	MatchesByPass          map[Pass]int `json:"matches_by_pass"`
	MatchesByKind          map[Kind]int `json:"matches_by_kind"`
	MatchedEntries         int          `json:"matched_entries"`
	MatchedExpected        int          `json:"matched_expected"`
	UnmatchedEntries       int          `json:"unmatched_entries"`
	UnmatchedExpected      int          `json:"unmatched_expected"`
	DuplicateGroups        int          `json:"duplicate_groups"`
	EntryCreditCents       int64        `json:"entry_credit_cents"`
	EntryDebitCents        int64        `json:"entry_debit_cents"`
	UnmatchedEntryCents    int64        `json:"unmatched_entry_cents"`
	UnmatchedExpectedCents int64        `json:"unmatched_expected_cents"`
	// DifferenceCents sums DiffCents over all matches (fees withheld,
	// interest paid, rounding).
	DifferenceCents int64 `json:"difference_cents"`
	// GroupSearchTruncated counts subset searches that hit their node or
	// time budget; a non-zero value means a larger budget might find more
	// groups.
	GroupSearchTruncated int `json:"group_search_truncated"`
}

// Result is the outcome of a reconciliation. Matches are ordered by pass,
// then by the position of their first statement entry; unmatched records
// keep input order. The same input and configuration always produce the
// same Result.
type Result struct {
	Matches           []Match           `json:"matches"`
	UnmatchedEntries  []statement.Entry `json:"unmatched_entries"`
	UnmatchedExpected []Expected        `json:"unmatched_expected"`
	Duplicates        []DuplicateGroup  `json:"duplicates"`
	Summary           Summary           `json:"summary"`
}
