// internal/adapter/javabuild_test.go
package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectJavaBuildTool_mavenWithoutWrapper(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project></project>"), 0o644)

	bt := detectJavaBuildTool(dir)
	if bt.kind != "maven" || bt.command != "mvn" {
		t.Errorf("detectJavaBuildTool = %+v, want {maven, mvn}", bt)
	}
}

func TestDetectJavaBuildTool_mavenPrefersWrapper(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project></project>"), 0o644)
	os.WriteFile(filepath.Join(dir, "mvnw"), []byte("#!/bin/sh\n"), 0o755)

	bt := detectJavaBuildTool(dir)
	if bt.kind != "maven" || bt.command != "./mvnw" {
		t.Errorf("detectJavaBuildTool = %+v, want {maven, ./mvnw}", bt)
	}
}

func TestDetectJavaBuildTool_gradlePrefersWrapper(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "build.gradle"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "gradlew"), []byte("#!/bin/sh\n"), 0o755)

	bt := detectJavaBuildTool(dir)
	if bt.kind != "gradle" || bt.command != "./gradlew" {
		t.Errorf("detectJavaBuildTool = %+v, want {gradle, ./gradlew}", bt)
	}
}

func TestJavaModules_mavenParent(t *testing.T) {
	dir := t.TempDir()
	pom := "<project><modules><module>module-a</module><module>module-b</module></modules></project>"
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(pom), 0o644)

	modules := javaModules(dir)
	if len(modules) != 2 || modules[0] != "module-a" || modules[1] != "module-b" {
		t.Errorf("javaModules = %v, want [module-a module-b]", modules)
	}
}

func TestJavaModules_mavenSingleModuleReturnsNil(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project></project>"), 0o644)

	if modules := javaModules(dir); modules != nil {
		t.Errorf("javaModules on a single-module pom.xml = %v, want nil", modules)
	}
}

func TestJavaModules_gradleSettingsInclude(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.gradle"), []byte("include 'module-a', 'module-b'\n"), 0o644)

	modules := javaModules(dir)
	if len(modules) != 2 || modules[0] != "module-a" || modules[1] != "module-b" {
		t.Errorf("javaModules = %v, want [module-a module-b]", modules)
	}
}

func TestJavaModuleExcludeGlobs(t *testing.T) {
	dir := t.TempDir()
	pom := "<project><modules><module>module-a</module></modules></project>"
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(pom), 0o644)

	globs := javaModuleExcludeGlobs(dir)
	if len(globs) != 1 || globs[0] != "module-a/**" {
		t.Errorf("javaModuleExcludeGlobs = %v, want [module-a/**]", globs)
	}
}
