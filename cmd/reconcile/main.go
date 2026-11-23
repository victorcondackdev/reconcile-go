// Command reconcile matches bank statements against expected records.
//
//	reconcile run --statement extrato.ofx --expected pedidos.csv \
//	    [--mapping mapping.json] [--config rules.json] \
//	    [--json out.json|-] [--csv out.csv|-] [--details] [--fail-on-unmatched]
//	reconcile parse [--mapping mapping.json] [--format F] [--json] FILE
//	reconcile config
//
// The statement format (OFX 1.x/2.x, camt.052/053/054, CSV) is detected
// from the content. Expected records are read from CSV, or from a JSON
// array when the file name ends in .json. The mapping file holds the CSV
// layouts:
//
//	{"statement": {...statement.CSVMapping...},
//	 "expected":  {...match.ExpectedMapping...}}
//
// Exit status: 0 on success, 1 on errors, 2 on usage errors, 3 when
// --fail-on-unmatched is set and something was left unmatched.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/victorcondackdev/reconcile-go/internal/decimal"
	"github.com/victorcondackdev/reconcile-go/match"
	"github.com/victorcondackdev/reconcile-go/statement"
)

const version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage:
  reconcile run --statement FILE --expected FILE [--mapping FILE] [--config FILE]
                [--json OUT|-] [--csv OUT|-] [--details] [--fail-on-unmatched]
  reconcile parse [--mapping FILE] [--format ofx|csv|camt] [--json] FILE
  reconcile config     print the default matching configuration
  reconcile version
`

var errUsage = errors.New("usage")

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "run":
		var code int
		code, err = cmdRun(args[1:], stdout, stderr)
		if err == nil {
			return code
		}
	case "parse":
		err = cmdParse(args[1:], stdout, stderr)
	case "config":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		err = enc.Encode(match.DefaultConfig())
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "reconcile", version)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
	default:
		fmt.Fprintf(stderr, "reconcile: unknown command %q\n%s", args[0], usage)
		return 2
	}
	if errors.Is(err, errUsage) || errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "reconcile:", err)
		return 1
	}
	return 0
}

// parseInterspersed lets flags follow positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, fmt.Errorf("%w: %v", errUsage, err)
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

type mappingFile struct {
	Statement *statement.CSVMapping  `json:"statement,omitempty"`
	Expected  *match.ExpectedMapping `json:"expected,omitempty"`
}

func loadMapping(path string) (*mappingFile, error) {
	m := &mappingFile{}
	if path == "" {
		return m, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(m); err != nil {
		return nil, fmt.Errorf("mapping %s: %w", path, err)
	}
	return m, nil
}

func parseStatement(path string, m *mappingFile, format string) (*statement.Statement, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opts := statement.Options{CSV: m.Statement, Format: statement.Format(format)}
	st, err := statement.ParseWithOptions(f, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return st, nil
}

func cmdRun(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stmtPath := fs.String("statement", "", "statement file (OFX, camt, CSV)")
	expPath := fs.String("expected", "", "expected records (CSV, or JSON array if *.json)")
	mapPath := fs.String("mapping", "", "JSON mapping file for CSV inputs")
	cfgPath := fs.String("config", "", "JSON matching configuration (defaults apply to absent fields)")
	jsonOut := fs.String("json", "", "write the result as JSON to this file, or - for stdout")
	csvOut := fs.String("csv", "", "write the result as CSV to this file, or - for stdout")
	details := fs.Bool("details", false, "list every match with its explanation in the report")
	failUnmatched := fs.Bool("fail-on-unmatched", false, "exit with status 3 when anything is left unmatched")
	format := fs.String("format", "", "force the statement format (ofx, csv, camt)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return 0, err
	}
	if *stmtPath == "" || *expPath == "" {
		return 0, errUsage
	}
	m, err := loadMapping(*mapPath)
	if err != nil {
		return 0, err
	}
	cfg := match.DefaultConfig()
	if *cfgPath != "" {
		f, err := os.Open(*cfgPath)
		if err != nil {
			return 0, err
		}
		cfg, err = match.LoadConfig(f)
		f.Close()
		if err != nil {
			return 0, fmt.Errorf("%s: %w", *cfgPath, err)
		}
	}
	st, err := parseStatement(*stmtPath, m, *format)
	if err != nil {
		return 0, err
	}
	ef, err := os.Open(*expPath)
	if err != nil {
		return 0, err
	}
	var exp []match.Expected
	if strings.EqualFold(filepath.Ext(*expPath), ".json") {
		exp, err = match.ReadExpectedJSON(ef)
	} else {
		exp, err = match.ReadExpectedCSV(ef, m.Expected)
	}
	ef.Close()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", *expPath, err)
	}
	res, err := match.Reconcile(st.Entries, exp, cfg)
	if err != nil {
		return 0, err
	}
	toStdout := false
	write := func(path string, fn func(io.Writer) error) error {
		if path == "" {
			return nil
		}
		if path == "-" {
			toStdout = true
			return fn(stdout)
		}
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		if err := fn(f); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}
	if err := write(*jsonOut, res.WriteJSON); err != nil {
		return 0, err
	}
	if err := write(*csvOut, res.WriteCSV); err != nil {
		return 0, err
	}
	if !toStdout {
		if err := res.WriteReport(stdout, match.ReportOptions{Details: *details}); err != nil {
			return 0, err
		}
	}
	if *failUnmatched && (res.Summary.UnmatchedEntries > 0 || res.Summary.UnmatchedExpected > 0) {
		return 3, nil
	}
	return 0, nil
}

func cmdParse(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("parse", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mapPath := fs.String("mapping", "", "JSON mapping file for CSV input")
	asJSON := fs.Bool("json", false, "print the normalized statement as JSON")
	format := fs.String("format", "", "force the format (ofx, csv, camt)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errUsage
	}
	m, err := loadMapping(*mapPath)
	if err != nil {
		return err
	}
	st, err := parseStatement(pos[0], m, *format)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	cr, db := st.Totals()
	fmt.Fprintf(stdout, "format %s, account %s, currency %s, %d entries, credits %s, debits %s\n",
		st.Format, orDash(st.AccountID), orDash(st.Currency), len(st.Entries), decimal.Format(cr), decimal.Format(db))
	tw := tabwriter.NewWriter(stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tBOOKED\tAMOUNT\tCUR\tREFERENCE\tDESCRIPTION")
	for _, e := range st.Entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.ID, e.BookingDate.Format("2006-01-02"),
			decimal.Format(e.AmountCents), e.Currency, orDash(e.Reference), e.Description)
	}
	return tw.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
