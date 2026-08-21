package toolchain_test

import (
	"os"
	"path/filepath"
	"testing"

	"codebase-analyser/internal/toolchain"
)

func writePythonFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPythonDetect_notAPythonProject(t *testing.T) {
	dir := t.TempDir()
	if _, ok := (toolchain.Python{}).Detect(dir); ok {
		t.Error("ok = true for a directory with no Python manifest at all")
	}
}

func TestPythonDetect_setupPyOnlyIsAPythonProject(t *testing.T) {
	dir := t.TempDir()
	writePythonFile(t, dir, "setup.py", "from setuptools import setup\nsetup(name='x')\n")
	if _, ok := (toolchain.Python{}).Detect(dir); !ok {
		t.Error("ok = false for a setup.py-only project, want true")
	}
}

func TestPythonDetect_pythonVersionFileWins(t *testing.T) {
	dir := t.TempDir()
	writePythonFile(t, dir, "pyproject.toml", "[project]\nrequires-python = \">=3.9\"\n")
	writePythonFile(t, dir, ".python-version", "3.12.1\n")

	got, ok := toolchain.Python{}.Detect(dir)
	if !ok || got != "3.12.1" {
		t.Errorf("Detect = (%q, %v), want (3.12.1, true)", got, ok)
	}
}

func TestPythonDetect_requiresPythonLowerBound(t *testing.T) {
	dir := t.TempDir()
	writePythonFile(t, dir, "pyproject.toml", "[project]\nname = \"x\"\nrequires-python = \">=3.10,<4\"\n")

	got, ok := toolchain.Python{}.Detect(dir)
	if !ok || got != "3.10" {
		t.Errorf("Detect = (%q, %v), want (3.10, true)", got, ok)
	}
}

func TestPythonDetect_fallsBackToLatestStable(t *testing.T) {
	dir := t.TempDir()
	writePythonFile(t, dir, "requirements.txt", "requests==2.31.0\n")

	got, ok := toolchain.Python{}.Detect(dir)
	if !ok || got == "" {
		t.Errorf("Detect = (%q, %v), want a non-empty fallback version and ok=true", got, ok)
	}
}
