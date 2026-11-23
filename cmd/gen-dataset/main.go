// Command gen-dataset writes a synthetic, noisy reconciliation dataset with
// ground truth, and optionally scores the matcher against it.
//
//	gen-dataset -out DIR [-orders 500] [-noise 0.3] [-seed 1] [-format csv|ofx] [-eval]
//	gen-dataset -table [-orders 2000] [-seeds 5]
//
// DIR receives statement.csv (or statement.ofx), expected.csv,
// mapping.json, rules.json and truth.json. -table prints a Markdown table
// of precision and recall across increasing noise levels.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/victorcondackdev/reconcile-go/dataset"
	"github.com/victorcondackdev/reconcile-go/match"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gen-dataset", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "output directory")
	orders := fs.Int("orders", 500, "number of orders")
	noise := fs.Float64("noise", 0.3, "noise level from 0 (clean) to 1")
	seed := fs.Int64("seed", 1, "random seed")
	format := fs.String("format", "csv", "statement format: csv or ofx")
	eval := fs.Bool("eval", false, "run the matcher and print precision/recall")
	table := fs.Bool("table", false, "print a precision/recall table across noise levels")
	seeds := fs.Int("seeds", 5, "seeds averaged per row with -table")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *table {
		if err := printTable(stdout, *orders, *seeds); err != nil {
			fmt.Fprintln(stderr, "gen-dataset:", err)
			return 1
		}
		return 0
	}
	if *out == "" || (*format != "csv" && *format != "ofx") || *orders <= 0 {
		fs.Usage()
		return 2
	}
	d := dataset.Generate(dataset.Noisy(*noise, *orders, *seed))
	if err := write(*out, *format, d); err != nil {
		fmt.Fprintln(stderr, "gen-dataset:", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %d statement entries, %d expected records, %d truth links to %s\n",
		len(d.Entries), len(d.Expected), len(d.Truth), *out)
	if *eval {
		res, err := match.Reconcile(d.Entries, d.Expected, dataset.MatchConfig())
		if err != nil {
			fmt.Fprintln(stderr, "gen-dataset:", err)
			return 1
		}
		m := dataset.Evaluate(res, d)
		fmt.Fprintf(stdout, "precision %.4f  recall %.4f  F1 %.4f  (tp %d, fp %d, fn %d)\n",
			m.Precision, m.Recall, m.F1, m.TruePositives, m.FalsePositives, m.FalseNegatives)
	}
	return 0
}

func write(dir, format string, d *dataset.Dataset) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := []struct {
		name string
		fn   func(io.Writer) error
	}{
		{"expected.csv", d.WriteExpectedCSV},
		{"mapping.json", dataset.WriteMapping},
		{"truth.json", d.WriteTruth},
		{"rules.json", func(w io.Writer) error {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(dataset.MatchConfig())
		}},
	}
	if format == "ofx" {
		files = append(files, struct {
			name string
			fn   func(io.Writer) error
		}{"statement.ofx", d.WriteStatementOFX})
	} else {
		files = append(files, struct {
			name string
			fn   func(io.Writer) error
		}{"statement.csv", d.WriteStatementCSV})
	}
	for _, f := range files {
		fh, err := os.Create(filepath.Join(dir, f.name))
		if err != nil {
			return err
		}
		err = f.fn(fh)
		if cerr := fh.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
	}
	return nil
}

// Levels are the noise levels reported by -table.
var Levels = []float64{0, 0.1, 0.25, 0.5, 0.75, 1}

func printTable(w io.Writer, orders, seeds int) error {
	if orders <= 0 || seeds <= 0 {
		return errors.New("-orders and -seeds must be positive")
	}
	fmt.Fprintf(w, "| noise | entries | expected | precision | recall | F1 | F1, default rules | time |\n")
	fmt.Fprintf(w, "|------:|--------:|---------:|----------:|-------:|---:|------------------:|-----:|\n")
	for _, l := range Levels {
		var p, r, f, fd float64
		var ne, nx int
		var elapsed time.Duration
		for s := 1; s <= seeds; s++ {
			d := dataset.Generate(dataset.Noisy(l, orders, int64(s)))
			start := time.Now()
			res, err := match.Reconcile(d.Entries, d.Expected, dataset.MatchConfig())
			elapsed += time.Since(start)
			if err != nil {
				return err
			}
			m := dataset.Evaluate(res, d)
			p += m.Precision
			r += m.Recall
			f += m.F1
			ne += len(d.Entries)
			nx += len(d.Expected)
			// Same data with the default configuration, which knows
			// nothing about the acquirer's fee and settlement rules.
			res, err = match.Reconcile(d.Entries, d.Expected, match.DefaultConfig())
			if err != nil {
				return err
			}
			fd += dataset.Evaluate(res, d).F1
		}
		n := float64(seeds)
		fmt.Fprintf(w, "| %.2f | %d | %d | %.4f | %.4f | %.4f | %.4f | %s |\n", l, ne/seeds, nx/seeds, p/n, r/n, f/n, fd/n,
			(elapsed / time.Duration(seeds)).Round(100*time.Microsecond))
	}
	return nil
}
