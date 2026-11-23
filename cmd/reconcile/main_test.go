package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cli(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

const td = "../../testdata/"

func TestRunSamplesAgree(t *testing.T) {
	// The same statement in three formats reconciles the same way.
	var results []string
	for _, f := range []string{"extrato-ofx1.ofx", "extrato.csv", "extrato-camt053.xml"} {
		code, out, errs := cli("run", "--statement", td+f, "--expected", td+"pedidos.csv",
			"--mapping", td+"mapping.json", "--config", td+"rules.json", "--json", "-")
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", f, code, errs)
		}
		var res struct {
			Matches []struct {
				Pass        string   `json:"pass"`
				ExpectedIDs []string `json:"expected_ids"`
			} `json:"matches"`
			Summary struct {
				UnmatchedEntries  int `json:"unmatched_entries"`
				UnmatchedExpected int `json:"unmatched_expected"`
				DuplicateGroups   int `json:"duplicate_groups"`
			} `json:"summary"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var b strings.Builder
		for _, m := range res.Matches {
			b.WriteString(m.Pass + ":" + strings.Join(m.ExpectedIDs, "+") + " ")
		}
		if res.Summary.UnmatchedEntries != 2 || res.Summary.UnmatchedExpected != 1 || res.Summary.DuplicateGroups != 1 {
			t.Errorf("%s: summary %+v", f, res.Summary)
		}
		results = append(results, b.String())
	}
	want := "reference:PED-1001 reference:PED-1003 reference:PED-1008 exact:PED-1002 exact:PAG-0042 exact:PED-1009 " +
		"group:PED-1004+PED-1005+PED-1006 group:PED-1007 group:PED-1013+PED-1014 fuzzy:PED-1012 "
	for i, r := range results {
		if r != want {
			t.Errorf("format %d: %s\nwant %s", i, r, want)
		}
	}
}

func TestRunReportGolden(t *testing.T) {
	code, out, errs := cli("run", "--statement", td+"extrato-ofx1.ofx", "--expected", td+"pedidos.csv",
		"--mapping", td+"mapping.json", "--config", td+"rules.json", "--details")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	golden := filepath.Join(td, "golden", "report.txt")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		os.MkdirAll(filepath.Dir(golden), 0o755)
		os.WriteFile(golden, []byte(out), 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Errorf("report differs from %s (UPDATE_GOLDEN=1 to refresh):\n%s", golden, out)
	}
}

func TestRunOutputsAndFailOnUnmatched(t *testing.T) {
	dir := t.TempDir()
	jp, cp := filepath.Join(dir, "r.json"), filepath.Join(dir, "r.csv")
	code, out, errs := cli("run", "--statement", td+"extrato-ofx1.ofx", "--expected", td+"pedidos.csv",
		"--mapping", td+"mapping.json", "--json", jp, "--csv", cp, "--fail-on-unmatched")
	if code != 3 {
		t.Fatalf("exit %d, want 3: %s", code, errs)
	}
	if !strings.Contains(out, "Reconciliation report") {
		t.Errorf("report not printed when outputs go to files")
	}
	if b, err := os.ReadFile(jp); err != nil || !json.Valid(b) {
		t.Errorf("json output: %v", err)
	}
	f, _ := os.Open(cp)
	rows, err := csv.NewReader(f).ReadAll()
	f.Close()
	if err != nil || len(rows) < 5 || rows[0][0] != "status" {
		t.Errorf("csv output: %v %v", rows, err)
	}
	code, out, _ = cli("run", "--statement", td+"extrato.csv", "--expected", td+"pedidos.csv", "--mapping", td+"mapping.json", "--csv", "-")
	if code != 0 || !strings.HasPrefix(out, "status,match_id") {
		t.Errorf("csv to stdout: %d %q", code, out[:min(len(out), 80)])
	}
}

func TestRunExpectedJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "exp.json")
	os.WriteFile(p, []byte(`[{"id":"PED-1009","amount_cents":64000,"date":"2026-11-19T00:00:00Z","counterparty":"Ricardo Almeida"}]`), 0o644)
	code, out, errs := cli("run", "--statement", td+"extrato-ofx2.ofx", "--expected", p, "--format", "ofx")
	if code != 0 || !strings.Contains(out, "Unmatched expected        1") {
		t.Errorf("exit %d %s %s", code, out, errs)
	}
}

func TestParseCommand(t *testing.T) {
	code, out, errs := cli("parse", td+"extrato-camt054.xml")
	if code != 0 || !strings.Contains(out, "format camt.054") || !strings.Contains(out, "CN-554433") {
		t.Errorf("parse: %d %s %s", code, out, errs)
	}
	code, out, _ = cli("parse", td+"extrato.csv", "--mapping", td+"mapping.json", "--json")
	var st struct {
		Format  string            `json:"format"`
		Entries []json.RawMessage `json:"entries"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || st.Format != "csv" || len(st.Entries) != 13 {
		t.Errorf("parse --json: %d %s", code, out)
	}
}

func TestCLIErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"nope":1}`), 0o644)
	tests := []struct {
		args []string
		code int
		msg  string
	}{
		{nil, 2, "usage"},
		{[]string{"frobnicate"}, 2, "unknown command"},
		{[]string{"run"}, 2, "usage"},
		{[]string{"run", "--bogus"}, 2, "usage"},
		{[]string{"parse"}, 2, "usage"},
		{[]string{"parse", "a", "b"}, 2, "usage"},
		{[]string{"parse", filepath.Join(dir, "missing.ofx")}, 1, "no such file"},
		{[]string{"parse", "--mapping", bad, td + "extrato.csv"}, 1, "mapping"},
		{[]string{"parse", "--mapping", filepath.Join(dir, "none.json"), td + "extrato.csv"}, 1, "no such file"},
		{[]string{"parse", td + "pedidos.csv", "--format", "camt"}, 1, "syntax error"},
		{[]string{"run", "--statement", td + "extrato-ofx1.ofx", "--expected", td + "pedidos.csv", "--config", bad}, 1, "config"},
		{[]string{"run", "--statement", td + "extrato-ofx1.ofx", "--expected", td + "pedidos.csv", "--config", filepath.Join(dir, "x")}, 1, "no such file"},
		{[]string{"run", "--statement", td + "extrato-ofx1.ofx", "--expected", filepath.Join(dir, "x.csv")}, 1, "no such file"},
		{[]string{"run", "--statement", td + "extrato-ofx1.ofx", "--expected", bad}, 1, "expected json"},
		{[]string{"run", "--statement", filepath.Join(dir, "x"), "--expected", td + "pedidos.csv"}, 1, "no such file"},
		{[]string{"run", "--statement", td + "extrato-ofx1.ofx", "--expected", td + "pedidos.csv", "--mapping", bad}, 1, "mapping"},
		{[]string{"run", "--statement", td + "extrato-ofx1.ofx", "--expected", td + "pedidos.csv", "--json", filepath.Join(dir, "no", "such", "dir.json")}, 1, "no such file"},
		{[]string{"run", "--statement", td + "extrato-ofx1.ofx", "--expected", td + "extrato.csv"}, 1, "cannot infer"},
	}
	for _, tt := range tests {
		code, _, errs := cli(tt.args...)
		if code != tt.code || !strings.Contains(errs, tt.msg) {
			t.Errorf("%v: exit %d, stderr %q; want %d containing %q", tt.args, code, errs, tt.code, tt.msg)
		}
	}
}

func TestMiscCommands(t *testing.T) {
	if code, out, _ := cli("version"); code != 0 || !strings.Contains(out, version) {
		t.Errorf("version: %d %s", code, out)
	}
	if code, out, _ := cli("help"); code != 0 || !strings.Contains(out, "usage") {
		t.Errorf("help: %d %s", code, out)
	}
	code, out, _ := cli("config")
	var v map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v["passes"] == nil {
		t.Errorf("config: %d %s", code, out)
	}
	if orDash("") != "-" || orDash("x") != "x" {
		t.Error("orDash")
	}
}
