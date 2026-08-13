package toolchain_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"codebase-analyser/internal/toolchain"
)

func writeMod(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGoDetectReadsTheGoDirective(t *testing.T) {
	cases := []struct {
		name, mod, want string
		wantOK          bool
	}{
		{"patch version", "module m\n\ngo 1.26.5\n", "1.26.5", true},
		{"minor version", "module m\n\ngo 1.22\n", "1.22", true},
		{"toolchain line is not the go directive", "module m\n\ngo 1.22\n\ntoolchain go1.26.5\n", "1.22", true},
		{"tabs and trailing comment", "module m\n\ngo\t1.23.1 // pinned\n", "1.23.1", true},
		{"no directive", "module m\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toolchain.Go{}.Detect(writeMod(t, tc.mod))
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("Detect = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestGoDetectOnANonGoDirectory(t *testing.T) {
	if _, ok := (toolchain.Go{}).Detect(t.TempDir()); ok {
		t.Error("ok = true for a directory with no go.mod")
	}
}

func TestGoEnsureSetsGOTOOLCHAIN(t *testing.T) {
	env, err := toolchain.Go{}.Ensure("1.26.5")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "GOTOOLCHAIN=go1.26.5") {
		t.Errorf("env = %v, want it to contain GOTOOLCHAIN=go1.26.5", env)
	}
}

func TestEnvForARepoWithNoDeclaredVersionIsEmpty(t *testing.T) {
	if env := toolchain.Env(t.TempDir()); len(env) != 0 {
		t.Errorf("Env = %v, want empty when nothing is declared (fall back to whatever is installed)", env)
	}
}

func TestEnvForAGoRepo(t *testing.T) {
	dir := writeMod(t, "module m\n\ngo 1.26.5\n")
	if env := toolchain.Env(dir); !slices.Contains(env, "GOTOOLCHAIN=go1.26.5") {
		t.Errorf("Env = %v, want GOTOOLCHAIN=go1.26.5", env)
	}
}
