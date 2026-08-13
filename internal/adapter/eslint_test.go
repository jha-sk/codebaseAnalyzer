package adapter

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codebase-analyser/internal/finding"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestESLintFindings_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/eslint_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := eslintFindings(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 4 {
		t.Fatalf("got %d findings, want 4", len(findings))
	}

	f := findings[0]
	if f.File != "/repo/src/a.js" || f.Line != 12 || f.Tool != "eslint" || f.RuleID != "no-eval" {
		t.Errorf("finding[0] = %+v", f)
	}
	if f.Category != finding.CategorySecurity || f.Severity != finding.SeverityCritical {
		t.Errorf("finding[0] category/severity = %v/%v, want security/critical", f.Category, f.Severity)
	}
	if f.Message != "eval can be harmful." {
		t.Errorf("finding[0].Message = %q", f.Message)
	}

	f = findings[1]
	if f.File != "/repo/src/a.js" || f.Line != 5 || f.RuleID != "no-unused-vars" {
		t.Errorf("finding[1] = %+v", f)
	}
	if f.Category != finding.CategoryCorrectness || f.Severity != finding.SeverityLow {
		t.Errorf("finding[1] category/severity = %v/%v, want correctness/low", f.Category, f.Severity)
	}

	// Fatal parse error (null ruleId): reported under RuleID "fatal", always
	// correctness/high regardless of ESLint's own severity number.
	f = findings[2]
	if f.File != "/repo/src/b.ts" || f.Line != 1 || f.RuleID != "fatal" {
		t.Errorf("finding[2] = %+v, want fatal parse error on b.ts:1", f)
	}
	if f.Category != finding.CategoryCorrectness || f.Severity != finding.SeverityHigh {
		t.Errorf("finding[2] category/severity = %v/%v, want correctness/high", f.Category, f.Severity)
	}
	if !strings.Contains(f.Message, "Parsing error") {
		t.Errorf("finding[2].Message = %q, want it to mention the parse error", f.Message)
	}

	// /repo/src/c.js had an empty "messages" array and must be skipped
	// entirely, not turn into a zero-value Finding.
	for _, f := range findings {
		if f.File == "/repo/src/c.js" {
			t.Errorf("clean file c.js produced a finding: %+v", f)
		}
	}

	f = findings[3]
	if f.File != "/repo/src/d.js" || f.Line != 20 || f.RuleID != "security/detect-child-process" {
		t.Errorf("finding[3] = %+v", f)
	}
	if f.Category != finding.CategorySecurity || f.Severity != finding.SeverityHigh {
		t.Errorf("finding[3] category/severity = %v/%v, want security/high", f.Category, f.Severity)
	}
}

func TestESLintFindings_emptyArray(t *testing.T) {
	findings, err := eslintFindings(strings.NewReader("[]"))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("got %d findings, want 0", len(findings))
	}
}

func TestESLintFindings_malformedJSON(t *testing.T) {
	_, err := eslintFindings(strings.NewReader("{not valid json"))
	if err == nil {
		t.Fatal("want error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "eslint: parsing output:") {
		t.Errorf("err = %q, want it to contain %q", err.Error(), "eslint: parsing output:")
	}
}

func TestRepoHasESLintConfig(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  bool
	}{
		{
			name: "flat config",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "eslint.config.js"), "export default [];")
			},
			want: true,
		},
		{
			name: "legacy dotfile",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, ".eslintrc.json"), "{}")
			},
			want: true,
		},
		{
			name: "eslintConfig key in package.json",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "package.json"), `{"name":"x","eslintConfig":{"rules":{}}}`)
			},
			want: true,
		},
		{
			name:  "bare dir",
			setup: func(t *testing.T, dir string) {},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)
			if got := repoHasESLintConfig(dir); got != tt.want {
				t.Errorf("repoHasESLintConfig(%q) = %v, want %v", dir, got, tt.want)
			}
		})
	}
}

func TestWorkspaceIgnoreGlobs(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  []string
	}{
		{
			name: "array form",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "package.json"), `{"name":"root","workspaces":["packages/*","apps/*"]}`)
			},
			want: []string{"packages/*", "apps/*"},
		},
		{
			name: "object form",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "package.json"), `{"name":"root","workspaces":{"packages":["libs/*"]}}`)
			},
			want: []string{"libs/*"},
		},
		{
			name: "pnpm-workspace.yaml quoted",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "pnpm-workspace.yaml"), "packages:\n  - \"apps/*\"\n  - \"packages/*\"\n")
			},
			want: []string{"apps/*", "packages/*"},
		},
		{
			name: "pnpm-workspace.yaml unquoted",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "pnpm-workspace.yaml"), "packages:\n  - apps/*\n  - packages/*\n")
			},
			want: []string{"apps/*", "packages/*"},
		},
		{
			name: "pnpm-workspace.yaml commented header",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "pnpm-workspace.yaml"), "packages: # workspace list\n  - \"apps/*\"\n  - \"packages/*\"\n")
			},
			want: []string{"apps/*", "packages/*"},
		},
		{
			name: "pnpm-workspace.yaml trailing comment on item",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "pnpm-workspace.yaml"), "packages:\n  - \"apps/*\" # apps workspace\n  - \"packages/*\"\n")
			},
			want: []string{"apps/*", "packages/*"},
		},
		{
			name: "pnpm-workspace.yaml full-line comment inside block",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "pnpm-workspace.yaml"), "packages:\n  # a comment on its own line\n  - \"apps/*\"\n  - \"packages/*\"\n")
			},
			want: []string{"apps/*", "packages/*"},
		},
		{
			name:  "none",
			setup: func(t *testing.T, dir string) {},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)
			got := workspaceIgnoreGlobs(dir)
			if len(got) != len(tt.want) {
				t.Fatalf("workspaceIgnoreGlobs(%q) = %v, want %v", dir, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("workspaceIgnoreGlobs(%q)[%d] = %q, want %q", dir, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestESLintMajorVersion(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"v9.39.0", 9},
		{"8.57.1", 8},
		{"garbage", 0},
	}
	for _, tt := range tests {
		if got := eslintMajorVersion(tt.in); got != tt.want {
			t.Errorf("eslintMajorVersion(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
