package main

import (
	"bytes"
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

func TestGenerate(t *testing.T) {
	for _, format := range []string{"csv", "ofx"} {
		dir := t.TempDir()
		code, out, errs := cli("-out", dir, "-orders", "120", "-noise", "0.4", "-seed", "3", "-format", format, "-eval")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errs)
		}
		if !strings.Contains(out, "precision") || !strings.Contains(out, "wrote") {
			t.Errorf("output %q", out)
		}
		for _, f := range []string{"expected.csv", "mapping.json", "truth.json", "rules.json", "statement." + format} {
			if st, err := os.Stat(filepath.Join(dir, f)); err != nil || st.Size() == 0 {
				t.Errorf("%s: %v", f, err)
			}
		}
	}
}

func TestTable(t *testing.T) {
	code, out, errs := cli("-table", "-orders", "60", "-seeds", "1")
	if code != 0 || strings.Count(out, "\n") != len(Levels)+2 || !strings.HasPrefix(out, "| noise |") {
		t.Errorf("exit %d %q %s", code, out, errs)
	}
	if code, _, _ := cli("-table", "-seeds", "0"); code != 1 {
		t.Errorf("seeds 0: exit %d", code)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"-out", "x", "-format", "xls"}, {"-out", "x", "-orders", "0"}, {"-bogus"}} {
		if code, _, _ := cli(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o644)
	if code, _, _ := cli("-out", filepath.Join(f, "sub")); code != 1 {
		t.Errorf("unwritable dir: exit %d", code)
	}
}
