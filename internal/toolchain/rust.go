package toolchain

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// channelKey matches the `channel = "..."` entry of rust-toolchain.toml.
// A three-line regex beats a TOML dependency for one key in one file.
var channelKey = regexp.MustCompile(`(?m)^[ \t]*channel[ \t]*=[ \t]*["']([^"']+)["']`)

// Rust resolves the toolchain declared by rust-toolchain.toml, or by the
// legacy bare `rust-toolchain` file.
type Rust struct{}

func (Rust) Detect(repoPath string) (string, bool) {
	if b, err := os.ReadFile(filepath.Join(repoPath, "rust-toolchain.toml")); err == nil {
		if m := channelKey.FindSubmatch(b); m != nil {
			return string(m[1]), true
		}
		return "", false
	}
	// The legacy file is the bare toolchain name, nothing else.
	b, err := os.ReadFile(filepath.Join(repoPath, "rust-toolchain"))
	if err != nil {
		return "", false
	}
	name := strings.TrimSpace(string(b))
	if name == "" || strings.ContainsAny(name, "\n[=") {
		return "", false
	}
	return name, true
}

// Ensure leans on rustup, which installs a missing toolchain on first use
// when RUSTUP_TOOLCHAIN names it. Same reasoning as Go's GOTOOLCHAIN: the
// language ships this machinery, so we do not rebuild it.
func (Rust) Ensure(version string) ([]string, error) {
	return []string{"RUSTUP_TOOLCHAIN=" + version}, nil
}
