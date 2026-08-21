// internal/adapter/javabuild.go
package adapter

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// javaBuildTool identifies which build tool a Java project uses and which
// invocation to prefer: the project's own wrapper (./mvnw, ./gradlew) when
// present — matches exactly what the team's CI and local builds already
// use, avoiding version-mismatch build failures (spec: "Build tool
// invocation prefers the project's own wrapper... when present"). Falls
// back to a bare "mvn"/"gradle" resolved via PATH otherwise.
type javaBuildTool struct {
	kind    string // "maven" or "gradle"
	command string // "./mvnw", "./gradlew", "mvn", or "gradle"
}

// detectJavaBuildTool inspects path (a project directory detect.Detect
// already confirmed has a pom.xml or build.gradle[.kts]) and reports which
// build tool and invocation to use. A pom.xml takes priority over a
// build.gradle in the same directory — the same Maven-primary dedup
// detect.Detect itself applies (internal/detect/detect.go).
func detectJavaBuildTool(path string) javaBuildTool {
	if _, err := os.Stat(filepath.Join(path, "pom.xml")); err == nil {
		if isExecutable(filepath.Join(path, "mvnw")) {
			return javaBuildTool{kind: "maven", command: "./mvnw"}
		}
		return javaBuildTool{kind: "maven", command: "mvn"}
	}
	if isExecutable(filepath.Join(path, "gradlew")) {
		return javaBuildTool{kind: "gradle", command: "./gradlew"}
	}
	return javaBuildTool{kind: "gradle", command: "gradle"}
}

// mavenModuleEntry matches one <module>child</module> element of a parent
// POM's <modules> list.
var mavenModuleEntry = regexp.MustCompile(`<module>\s*([^<\s]+)\s*</module>`)

// gradleIncludeEntry matches one entry of a Gradle settings file's
// `include 'a', 'b'` (or Kotlin DSL `include(":a")`) statement — single or
// double quoted, with or without a leading ":".
var gradleIncludeEntry = regexp.MustCompile(`['"]:?([a-zA-Z0-9_\-./]+)['"]`)

// javaModules lists path's direct child modules, read from the parent
// POM's <modules> list or Gradle's settings.gradle[.kts] `include`
// statements. Returns nil for a single-module project (no <modules>/
// include found) — most Java projects are single-module, and nil here is
// the normal case, not an error.
func javaModules(path string) []string {
	if b, err := os.ReadFile(filepath.Join(path, "pom.xml")); err == nil {
		matches := mavenModuleEntry.FindAllSubmatch(b, -1)
		if len(matches) == 0 {
			return nil
		}
		modules := make([]string, len(matches))
		for i, m := range matches {
			modules[i] = string(m[1])
		}
		return modules
	}
	for _, name := range []string{"settings.gradle", "settings.gradle.kts"} {
		b, err := os.ReadFile(filepath.Join(path, name))
		if err != nil {
			continue
		}
		return gradleIncludedModules(string(b))
	}
	return nil
}

func gradleIncludedModules(src string) []string {
	var modules []string
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "include") {
			continue
		}
		for _, m := range gradleIncludeEntry.FindAllStringSubmatch(trimmed, -1) {
			modules = append(modules, strings.ReplaceAll(m[1], ":", "/"))
		}
	}
	return modules
}

// javaModuleExcludeGlobs returns the glob patterns PMD/Checkstyle must
// exclude when scanning path, so a root-level (aggregator) run does not
// double-report findings that each child module's own separately-detected
// Project already reports (detect.Detect picks up every module's own
// pom.xml/build.gradle as its own Project — see Task 2). Mirrors
// workspaceIgnoreGlobs's exact job for JS/TS workspaces (eslint.go).
func javaModuleExcludeGlobs(path string) []string {
	modules := javaModules(path)
	globs := make([]string, len(modules))
	for i, m := range modules {
		globs[i] = m + "/**"
	}
	return globs
}
