// internal/toolchain/java_test.go
package toolchain_test

import (
	"os"
	"path/filepath"
	"testing"

	"codebase-analyser/internal/toolchain"
)

func TestJavaDetect_notAJavaProject(t *testing.T) {
	root := t.TempDir()
	if _, ok := (toolchain.Java{}).Detect(root); ok {
		t.Error("Detect on an empty dir = ok, want false")
	}
}

func TestJavaDetect_javaVersionFileWins(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "pom.xml"), []byte("<project><modelVersion>4.0.0</modelVersion></project>"), 0o644)
	os.WriteFile(filepath.Join(root, ".java-version"), []byte("17\n"), 0o644)

	v, ok := (toolchain.Java{}).Detect(root)
	if !ok || v != "17" {
		t.Fatalf("Detect = (%q, %v), want (\"17\", true)", v, ok)
	}
}

func TestJavaDetect_mavenReleaseElement(t *testing.T) {
	root := t.TempDir()
	pom := `<project><build><plugins><plugin>
		<configuration><release>21</release></configuration>
	</plugin></plugins></build></project>`
	os.WriteFile(filepath.Join(root, "pom.xml"), []byte(pom), 0o644)

	v, ok := (toolchain.Java{}).Detect(root)
	if !ok || v != "21" {
		t.Fatalf("Detect = (%q, %v), want (\"21\", true)", v, ok)
	}
}

func TestJavaDetect_mavenLegacySourceElementNormalized(t *testing.T) {
	root := t.TempDir()
	pom := `<project><build><plugins><plugin>
		<configuration><source>1.8</source></configuration>
	</plugin></plugins></build></project>`
	os.WriteFile(filepath.Join(root, "pom.xml"), []byte(pom), 0o644)

	v, ok := (toolchain.Java{}).Detect(root)
	if !ok || v != "8" {
		t.Fatalf("Detect = (%q, %v), want (\"8\", true) — legacy 1.8 must normalize to 8", v, ok)
	}
}

func TestJavaDetect_gradleToolchainBlock(t *testing.T) {
	root := t.TempDir()
	build := "java {\n    toolchain {\n        languageVersion = JavaLanguageVersion.of(17)\n    }\n}\n"
	os.WriteFile(filepath.Join(root, "build.gradle"), []byte(build), 0o644)

	v, ok := (toolchain.Java{}).Detect(root)
	if !ok || v != "17" {
		t.Fatalf("Detect = (%q, %v), want (\"17\", true)", v, ok)
	}
}

func TestJavaDetect_gradleSourceCompatibility(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "build.gradle.kts"), []byte("sourceCompatibility = \"17\"\n"), 0o644)

	v, ok := (toolchain.Java{}).Detect(root)
	if !ok || v != "17" {
		t.Fatalf("Detect = (%q, %v), want (\"17\", true)", v, ok)
	}
}

func TestJavaDetect_fallsBackToLatestLTS(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "pom.xml"), []byte("<project></project>"), 0o644)

	v, ok := (toolchain.Java{}).Detect(root)
	if !ok || v != "21" {
		t.Fatalf("Detect = (%q, %v), want (\"21\", true)", v, ok)
	}
}

func TestJavaEnsure_unknownVersionWithNoSDKMANFailsClearly(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no ~/.sdkman here
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())

	_, err := (toolchain.Java{}).Ensure("999")
	if err == nil {
		t.Fatal("Ensure(\"999\") = nil error, want a clear failure (no pinned JDK build known)")
	}
}
