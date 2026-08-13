package toolchain_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"codebase-analyser/internal/toolchain"
)

func writeToolchainFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRustDetect(t *testing.T) {
	cases := []struct {
		name, file, content, want string
		wantOK                    bool
	}{
		{"table form", "rust-toolchain.toml", "[toolchain]\nchannel = \"1.78.0\"\n", "1.78.0", true},
		{"table form, single quotes", "rust-toolchain.toml", "[toolchain]\nchannel = '1.78.0'\n", "1.78.0", true},
		{"table form with components", "rust-toolchain.toml", "[toolchain]\nchannel = \"stable\"\ncomponents = [\"clippy\"]\n", "stable", true},
		{"legacy bare file", "rust-toolchain", "1.78.0\n", "1.78.0", true},
		{"no channel key", "rust-toolchain.toml", "[toolchain]\ncomponents = [\"clippy\"]\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toolchain.Rust{}.Detect(writeToolchainFile(t, tc.file, tc.content))
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("Detect = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestRustDetectWithNoToolchainFile(t *testing.T) {
	// Parenthesised: a composite literal in an if-init clause is parsed as
	// the start of the block, so `toolchain.Rust{}.Detect(...)` there is a
	// compile error. Same applies anywhere else in this plan.
	if _, ok := (toolchain.Rust{}).Detect(t.TempDir()); ok {
		t.Error("ok = true with no rust-toolchain file present")
	}
}

func TestRustEnsureSetsRUSTUPTOOLCHAIN(t *testing.T) {
	env, err := toolchain.Rust{}.Ensure("1.78.0")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "RUSTUP_TOOLCHAIN=1.78.0") {
		t.Errorf("env = %v, want RUSTUP_TOOLCHAIN=1.78.0", env)
	}
}

func TestEnvForARustRepo(t *testing.T) {
	dir := writeToolchainFile(t, "rust-toolchain.toml", "[toolchain]\nchannel = \"1.78.0\"\n")
	if env := toolchain.Env(dir); !slices.Contains(env, "RUSTUP_TOOLCHAIN=1.78.0") {
		t.Errorf("Env = %v, want RUSTUP_TOOLCHAIN=1.78.0", env)
	}
}
