package toolchain

// Rust resolves the toolchain declared by rust-toolchain.toml. Task 8 fills
// in Detect; until then a Rust repository simply uses the installed
// toolchain, which is the same behaviour the analyser has today.
type Rust struct{}

func (Rust) Detect(repoPath string) (string, bool) { return "", false }

func (Rust) Ensure(version string) ([]string, error) { return nil, nil }
