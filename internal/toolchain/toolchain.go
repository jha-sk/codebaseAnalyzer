// Package toolchain makes each analysis run against the language version the
// repository declares, rather than whatever happens to be installed.
// Analysing with the wrong version produces false positives (valid syntax
// flagged) and false negatives (a real deprecation missed).
package toolchain

// Resolver detects the language version a repository declares and produces
// the environment needed to run tools at that version. It is an interface
// because Java, Python, JS/TS and Next.js support are on the roadmap: adding
// one is a new implementation of this shape, not a rework.
type Resolver interface {
	// Detect reads the repository's declared version (go.mod's go directive,
	// rust-toolchain.toml, ...). ok is false when nothing is declared, which
	// means "use the latest stable / whatever is installed".
	Detect(repoPath string) (version string, ok bool)

	// Ensure returns the environment variables that make tools run at
	// version, downloading the toolchain first if that is what it takes.
	Ensure(version string) (env []string, err error)
}

// resolvers is every language this build knows about. Go and Rust ship in
// v1; the interface above is what keeps adding a third from being a rewrite.
var resolvers = []Resolver{Go{}, Rust{}}

// Env returns the extra environment for running tools against repoPath. A
// repository that declares nothing gets an empty environment: falling back to
// the installed toolchain is correct, and quieter than guessing.
//
// A resolver that fails to Ensure is skipped rather than fatal: running the
// analysis at the wrong version still finds real bugs, whereas not running it
// at all finds none.
func Env(repoPath string) []string {
	var env []string
	for _, r := range resolvers {
		version, ok := r.Detect(repoPath)
		if !ok {
			continue
		}
		vars, err := r.Ensure(version)
		if err != nil {
			continue
		}
		env = append(env, vars...)
	}
	return env
}
