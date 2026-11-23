package match

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/victorcondackdev/reconcile-go/internal/decimal"
)

// WriteJSON writes r as indented JSON.
func (r *Result) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// CSVHeader is the header row written by WriteCSV.
var CSVHeader = []string{
	"status", "match_id", "pass", "kind", "confidence", "entry_ids", "expected_ids",
	"entry_amount", "expected_amount", "diff", "rule",
}

// WriteCSV writes one row per match, then one row per unmatched entry
// ("unmatched_entry") and per unmatched expected record
// ("unmatched_expected"). Multiple IDs are joined with '|'; amounts use '.'
// as decimal separator.
func (r *Result) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(CSVHeader); err != nil {
		return err
	}
	for _, m := range r.Matches {
		if err := cw.Write([]string{
			"matched", m.ID, string(m.Pass), string(m.Kind), strconv.FormatFloat(m.Confidence, 'f', 3, 64),
			strings.Join(m.EntryIDs, "|"), strings.Join(m.ExpectedIDs, "|"),
			decimal.Format(m.EntryCents), decimal.Format(m.ExpectedCents), decimal.Format(m.DiffCents),
			m.Explanation.Rule,
		}); err != nil {
			return err
		}
	}
	for _, e := range r.UnmatchedEntries {
		if err := cw.Write([]string{"unmatched_entry", "", "", "", "", e.ID, "", decimal.Format(e.AmountCents), "", "", e.Description}); err != nil {
			return err
		}
	}
	for _, x := range r.UnmatchedExpected {
		if err := cw.Write([]string{"unmatched_expected", "", "", "", "", "", x.ID, "", decimal.Format(x.AmountCents), "", x.Counterparty}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ReportOptions controls WriteReport.
type ReportOptions struct {
	// Details lists every match with its features, not only the summary
	// and the exceptions.
	Details bool
	// MaxRows limits each listing (0 means 50; negative means unlimited).
	MaxRows int
}

// WriteReport writes a human-readable report: summary, matches by pass,
// exceptions (unmatched records, suspected duplicates, low-confidence
// matches) and, with Details, every match and its explanation.
func (r *Result) WriteReport(w io.Writer, opts ReportOptions) error {
	bw := bufio.NewWriter(w)
	limit := opts.MaxRows
	if limit == 0 {
		limit = 50
	}
	s := r.Summary
	p := func(format string, args ...any) { fmt.Fprintf(bw, format, args...) }
	p("Reconciliation report\n")
	p("=====================\n\n")
	p("Statement entries    %6d   credits %s, debits %s\n", s.Entries, decimal.FormatGrouped(s.EntryCreditCents, '.', ','), decimal.FormatGrouped(s.EntryDebitCents, '.', ','))
	p("Expected records     %6d\n", s.Expected)
	p("Matches              %6d   covering %d entries and %d expected records\n", s.Matches, s.MatchedEntries, s.MatchedExpected)
	for _, pass := range []Pass{PassReference, PassExact, PassGroup, PassFuzzy} {
		if n := s.MatchesByPass[pass]; n > 0 {
			p("  %-18s %6d\n", pass, n)
		}
	}
	if n := s.MatchesByKind[ManyToOne] + s.MatchesByKind[OneToMany]; n > 0 {
		p("  grouped: %d many-to-one, %d one-to-many\n", s.MatchesByKind[ManyToOne], s.MatchesByKind[OneToMany])
	}
	p("Net difference       %s (fees withheld, interest, rounding)\n", decimal.FormatGrouped(s.DifferenceCents, '.', ','))
	p("Unmatched entries    %6d   %s\n", s.UnmatchedEntries, decimal.FormatGrouped(s.UnmatchedEntryCents, '.', ','))
	p("Unmatched expected   %6d   %s\n", s.UnmatchedExpected, decimal.FormatGrouped(s.UnmatchedExpectedCents, '.', ','))
	p("Suspected duplicates %6d\n", s.DuplicateGroups)
	if s.GroupSearchTruncated > 0 {
		p("Group searches cut by budget: %d (raise group.max_nodes for a more exhaustive search)\n", s.GroupSearchTruncated)
	}

	rows := func(n int) int {
		if limit < 0 || n < limit {
			return n
		}
		return limit
	}
	more := func(n int) {
		if limit >= 0 && n > limit {
			p("  ... %d more\n", n-limit)
		}
	}
	if len(r.UnmatchedEntries) > 0 {
		p("\nUnmatched statement entries\n---------------------------\n")
		for _, e := range r.UnmatchedEntries[:rows(len(r.UnmatchedEntries))] {
			p("  %-24s %s %14s  %s\n", e.ID, e.BookingDate.Format("2006-01-02"), decimal.FormatGrouped(e.AmountCents, '.', ','), e.Description)
		}
		more(len(r.UnmatchedEntries))
	}
	if len(r.UnmatchedExpected) > 0 {
		p("\nUnmatched expected records\n--------------------------\n")
		for _, x := range r.UnmatchedExpected[:rows(len(r.UnmatchedExpected))] {
			d := "          "
			if !x.Date.IsZero() {
				d = x.Date.Format("2006-01-02")
			}
			p("  %-24s %s %14s  %s\n", x.ID, d, decimal.FormatGrouped(x.AmountCents, '.', ','), x.Counterparty)
		}
		more(len(r.UnmatchedExpected))
	}
	if len(r.Duplicates) > 0 {
		p("\nSuspected duplicates\n--------------------\n")
		for _, d := range r.Duplicates[:rows(len(r.Duplicates))] {
			p("  %-9s %s: %s\n", d.Side, strings.Join(d.IDs, ", "), d.Reason)
		}
		more(len(r.Duplicates))
	}
	var low []Match
	for _, m := range r.Matches {
		if m.Confidence < 0.7 {
			low = append(low, m)
		}
	}
	sort.SliceStable(low, func(a, b int) bool { return low[a].Confidence < low[b].Confidence })
	if len(low) > 0 {
		p("\nLow-confidence matches (review)\n-------------------------------\n")
		for _, m := range low[:rows(len(low))] {
			writeMatch(bw, m, false)
		}
		more(len(low))
	}
	if opts.Details && len(r.Matches) > 0 {
		p("\nMatches\n-------\n")
		for _, m := range r.Matches[:rows(len(r.Matches))] {
			writeMatch(bw, m, true)
		}
		more(len(r.Matches))
	}
	return bw.Flush()
}

func writeMatch(w io.Writer, m Match, features bool) {
	fmt.Fprintf(w, "  %s %-9s %.2f  entry %s %s <- expected %s %s\n", m.ID, m.Pass, m.Confidence,
		strings.Join(m.EntryIDs, "+"), decimal.FormatGrouped(m.EntryCents, '.', ','),
		strings.Join(m.ExpectedIDs, "+"), decimal.FormatGrouped(m.ExpectedCents, '.', ','))
	fmt.Fprintf(w, "        %s\n", m.Explanation.Rule)
	if features {
		for _, f := range m.Explanation.Features {
			if f.Weight > 0 {
				fmt.Fprintf(w, "        - %-12s %.2f x %.2f  %s\n", f.Name, f.Score, f.Weight, f.Detail)
			} else {
				fmt.Fprintf(w, "        - %-12s %s\n", f.Name, f.Detail)
			}
		}
	}
}
