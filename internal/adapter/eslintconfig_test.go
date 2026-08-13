package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepoHasESLintConfig_detectsAllRecognisedFormats exercises
// repoHasESLintConfig against three fixtures built exactly for this: one
// with a flat config (eslint.config.mjs), one with a legacy config
// (.eslintrc.json), and one with neither. This is deliberately a unit-level
// check on repoHasESLintConfig itself rather than a full ESLint run - a real
// run would exercise the same branch at far higher cost just to observe
// which config file got picked up.
func TestRepoHasESLintConfig_detectsAllRecognisedFormats(t *testing.T) {
	cases := []struct {
		dir  string
		want bool
	}{
		{"../../testdata/fixtures/js-flat-config", true},
		{"../../testdata/fixtures/js-legacy-config", true},
		{"../../testdata/fixtures/js-repo", false},
	}
	for _, c := range cases {
		if got := repoHasESLintConfig(c.dir); got != c.want {
			t.Errorf("repoHasESLintConfig(%q) = %v, want %v", c.dir, got, c.want)
		}
	}
}

// TestRepoHasESLintConfig_walksAncestors is Important 2's regression test: a
// monorepo whose ESLint config lives only at the workspace root previously
// had every member package report "no config of its own" (repoHasESLintConfig
// checked only the exact directory), so the analyser's baseline overrode the
// repo's real rules for every member. repoHasESLintConfig must now walk
// upward like findUp/jsBin already do, bounded at the directory containing
// .git so it can't wander into an unrelated config above the repo.
func TestRepoHasESLintConfig_walksAncestors(t *testing.T) {
	t.Run("config in the immediate dir", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "eslint.config.mjs"), []byte("export default []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !repoHasESLintConfig(dir) {
			t.Error("want true: config sits directly in dir")
		}
	})

	t.Run("config two levels up with .git at that level", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "eslint.config.mjs"), []byte("export default []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		member := filepath.Join(root, "packages", "a")
		if err := os.MkdirAll(member, 0o755); err != nil {
			t.Fatal(err)
		}
		if !repoHasESLintConfig(member) {
			t.Error("want true: root config two levels up, .git at the root")
		}
	})

	t.Run("no config anywhere below .git", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		member := filepath.Join(root, "packages", "a")
		if err := os.MkdirAll(member, 0o755); err != nil {
			t.Fatal(err)
		}
		if repoHasESLintConfig(member) {
			t.Error("want false: no config anywhere in the tree")
		}
	})

	t.Run("config above the .git boundary must not be found", func(t *testing.T) {
		outer := t.TempDir()
		if err := os.WriteFile(filepath.Join(outer, "eslint.config.mjs"), []byte("export default []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(outer, "repo")
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		member := filepath.Join(root, "packages", "a")
		if err := os.MkdirAll(member, 0o755); err != nil {
			t.Fatal(err)
		}
		if repoHasESLintConfig(member) {
			t.Error("want false: the config sits above the .git boundary and must not be found")
		}
	})
}
