package toolchain

import (
	"os"
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

// Ensure leans on Go's own toolchain switching: with GOTOOLCHAIN set to an
// explicit version, any Go 1.21+ command downloads and runs that version,
// caching it in the module cache. That is a supported, signed download path -
// there is no reason to reimplement it.
func (Go) Ensure(version string) ([]string, error) {
	return []string{"GOTOOLCHAIN=go" + version}, nil
}
