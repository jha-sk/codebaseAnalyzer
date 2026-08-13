package toolchain

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// goDirective matches go.mod's `go` directive and nothing else - notably not
// the `toolchain go1.x.y` line, which is a different statement with a
// different meaning.
var goDirective = regexp.MustCompile(`(?m)^go[ \t]+([0-9]+(?:\.[0-9]+){1,2}(?:(?:rc|beta)[0-9]+)?)`)

// Go resolves the version declared by a repository's go.mod.
type Go struct{}

func (Go) Detect(repoPath string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return "", false
	}
	m := goDirective.FindSubmatch(b)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

// Ensure prefers Go's own toolchain switching, which needs any Go 1.21+ on
// PATH: with GOTOOLCHAIN set to an explicit version, any such Go downloads
// and runs that version, caching it in the module cache. That is a
// supported, signed download path - there is no reason to reimplement it.
//
// With no Go at all on PATH, it falls back to a Go we download and manage
// ourselves, so the analyser works on a machine with no Go installed.
func (Go) Ensure(version string) ([]string, error) {
	if _, err := exec.LookPath("go"); err == nil {
		return []string{"GOTOOLCHAIN=go" + version}, nil
	}
	goroot, err := EnsureGo(version)
	if err != nil {
		return nil, err
	}
	return []string{
		"GOROOT=" + goroot,
		"PATH=" + filepath.Join(goroot, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, nil
}
