package toolchain

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// javaLatestLTS is used when a Java project declares no version of its own
// (neither .java-version, Maven's <release>/<source>, nor Gradle's java
// toolchain block). ponytail: hand-maintained constant; bump as new LTS
// releases ship — same reasoning pythonLatestStable documents.
const javaLatestLTS = "21"

// javaMarkers are the files that make a directory a Java project at all —
// mirrors detect.Detect's Java detection (internal/detect cannot be
// imported here, same reasoning pythonMarkers documents).
var javaMarkers = []string{"pom.xml", "build.gradle", "build.gradle.kts"}

// mavenReleaseKey matches the Maven compiler plugin's <release> or legacy
// <source> element, e.g. <release>17</release>. A regex beats a full XML
// dependency for one element in one file — same tradeoff requiresPythonKey
// makes for pyproject.toml.
var mavenReleaseKey = regexp.MustCompile(`<(?:release|source)>\s*([0-9]+(?:\.[0-9]+)?)\s*</(?:release|source)>`)

// gradleJavaVersionKey matches Gradle's `toolchain { languageVersion =
// JavaLanguageVersion.of(17) }` block or the shorter
// `sourceCompatibility = 17` form — both Groovy and Kotlin DSL use the same
// syntax for these two.
var gradleJavaVersionKey = regexp.MustCompile(`(?:languageVersion\s*=\s*JavaLanguageVersion\.of\(\s*([0-9]+)\s*\)|sourceCompatibility\s*=\s*['"]?([0-9]+(?:\.[0-9]+)?)['"]?)`)

// Java resolves the JDK version a repository declares.
type Java struct{}

// Detect mirrors Python's contract exactly: once a directory is confirmed
// to be a Java project at all, Detect always returns ok=true, falling back
// to javaLatestLTS rather than leaving the version unpinned.
func (Java) Detect(repoPath string) (string, bool) {
	if !isJavaProject(repoPath) {
		return "", false
	}
	if b, err := os.ReadFile(filepath.Join(repoPath, ".java-version")); err == nil {
		if v := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0]); v != "" {
			return normalizeJavaVersion(v), true
		}
	}
	if b, err := os.ReadFile(filepath.Join(repoPath, "pom.xml")); err == nil {
		if m := mavenReleaseKey.FindSubmatch(b); m != nil {
			return normalizeJavaVersion(string(m[1])), true
		}
	}
	for _, name := range []string{"build.gradle", "build.gradle.kts"} {
		b, err := os.ReadFile(filepath.Join(repoPath, name))
		if err != nil {
			continue
		}
		if m := gradleJavaVersionKey.FindSubmatch(b); m != nil {
			v := m[1]
			if len(v) == 0 {
				v = m[2]
			}
			return normalizeJavaVersion(string(v)), true
		}
	}
	return javaLatestLTS, true
}

// normalizeJavaVersion collapses legacy "1.8"-style version strings (still
// common in <source>/<release> and sourceCompatibility) to the modern
// major-only form ("8") EnsureJDK's download table is keyed by.
func normalizeJavaVersion(v string) string {
	if strings.HasPrefix(v, "1.") {
		return strings.TrimPrefix(v, "1.")
	}
	return v
}

func isJavaProject(repoPath string) bool {
	for _, name := range javaMarkers {
		if _, err := os.Stat(filepath.Join(repoPath, name)); err == nil {
			return true
		}
	}
	return false
}

// Ensure prefers SDKMAN, the reference implementation of "many JDK versions
// side by side" — same reasoning Python's Ensure gives for pyenv. With no
// SDKMAN install found, it falls back to EnsureJDK, a JDK downloaded and
// managed ourselves.
func (Java) Ensure(version string) ([]string, error) {
	if home := sdkmanCandidate(version); home != "" {
		return javaEnv(home), nil
	}
	root, err := EnsureJDK(version)
	if err != nil {
		return nil, err
	}
	return javaEnv(root), nil
}

func javaEnv(javaHome string) []string {
	return []string{
		"JAVA_HOME=" + javaHome,
		"PATH=" + filepath.Join(javaHome, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
}

// sdkmanCandidate returns SDKMAN's install root for version if SDKMAN is on
// this machine and already has that version installed. Unlike pyenv, "sdk"
// is a shell function sourced from the user's rc file, not a standalone
// executable — exec.Command("sdk", ...) can never find it — so this reads
// SDKMAN's own candidates directory directly instead, and only ever reuses
// an existing install (never triggers one, which "sdk install" requires an
// interactive shell for anyway).
func sdkmanCandidate(version string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".sdkman", "candidates", "java")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.Name() == version || strings.HasPrefix(e.Name(), version+".") {
			candidate := filepath.Join(dir, e.Name())
			if isExecutable(filepath.Join(candidate, "bin", exeName("java"))) {
				return candidate
			}
		}
	}
	return ""
}
