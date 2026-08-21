// internal/adapter/javatools.go
package adapter

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// javaToolKind is how a pinned tool's archive is packaged: PMD and OWASP
// Dependency-Check ship as .zip distributions, SpotBugs ships as .tar.gz,
// and Checkstyle ships as a single runnable "-all" jar with no archive at
// all.
type javaToolKind int

const (
	javaToolZip javaToolKind = iota
	javaToolTarGz
	javaToolJar
)

// javaToolAsset is one pinned tool's download — hand-verified url/sha256,
// same reasoning pyPinnedPackages documents for its own pins: exact
// versions kept reproducible across runs rather than floating.
//
// ponytail: url/sha256 below ship empty pending live verification (see
// this task's Step 6) — populate them by downloading each tool's current
// stable release once and recording its real sha256, the same gap
// pythonBuildAssets/jdkBuildAssets document for their own tables.
// CheckInstalled/Install fail clearly on an unpopulated entry rather than
// silently no-op'ing.
type javaToolAsset struct {
	version string
	url     string
	sha256  string
	kind    javaToolKind
}

var javaToolAssets = map[string]javaToolAsset{
	"pmd":              {kind: javaToolZip},
	"checkstyle":       {kind: javaToolJar},
	"spotbugs":         {kind: javaToolTarGz},
	"dependency-check": {kind: javaToolZip},
}

// javaToolsDir is where the analyser's own pinned PMD/Checkstyle/SpotBugs/
// Dependency-Check installs live — mirrors pyToolsDir/jsToolsDir's exact
// shape and cannot call cache.Root() either, for the same import-cycle
// reason documented there.
func javaToolsDir() string {
	if override := os.Getenv("CODEBASE_ANALYSER_CACHE"); override != "" {
		return filepath.Join(override, "java-tools")
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "codebase-analyser", "java-tools")
}

// pinnedJavaBin is the path to a tool's launcher script inside its own
// installed subdirectory, e.g. java-tools/pmd/bin/pmd.
func pinnedJavaBin(tool, relPath string) string {
	return filepath.Join(javaToolsDir(), tool, relPath)
}

// pinnedJavaJar is the path to a tool installed as a single runnable jar
// (Checkstyle) rather than an archive with its own launcher script.
func pinnedJavaJar(tool string) string {
	return filepath.Join(javaToolsDir(), tool, tool+".jar")
}

func javaJarInstalled(tool string) bool {
	info, err := os.Stat(pinnedJavaJar(tool))
	return err == nil && !info.IsDir()
}

var (
	javaInstallMu   sync.Mutex
	javaInstallDone = map[string]bool{}
)

// javaInstallStep performs one tool's actual install; a package-level func
// var (mirrors pyInstallStep) purely so tests can substitute a fake
// failing/succeeding step without touching the network.
var javaInstallStep = doInstallJavaTool

// installJavaTool installs one pinned tool, independently of the other
// three — a failure installing one must not block the others (spec: only
// SpotBugs is affected by its own upstream compile-step failure; PMD,
// Checkstyle and Dependency-Check must never be entangled with that).
// Mutex + a per-tool "done" map, same reasoning installPyTools documents
// for preferring that over sync.Once: a failure is retried by the next
// caller rather than poisoning every future analyze call in a long-running
// MCP server process.
func installJavaTool(tool string) error {
	javaInstallMu.Lock()
	defer javaInstallMu.Unlock()
	if javaInstallDone[tool] {
		return nil
	}
	if err := javaInstallStep(tool); err != nil {
		return err
	}
	javaInstallDone[tool] = true
	return nil
}

func doInstallJavaTool(tool string) error {
	asset, ok := javaToolAssets[tool]
	if !ok || asset.url == "" {
		return fmt.Errorf("no pinned download known for java tool %q", tool)
	}
	dir := filepath.Join(javaToolsDir(), tool)
	archive, err := downloadVerified(asset.url, asset.sha256)
	if err != nil {
		return fmt.Errorf("%s: %w", tool, err)
	}
	defer os.Remove(archive)

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("%s: %w", tool, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%s: %w", tool, err)
	}

	switch asset.kind {
	case javaToolJar:
		return copyToJar(archive, filepath.Join(dir, tool+".jar"))
	case javaToolZip:
		if err := extractZip(archive, dir); err != nil {
			return fmt.Errorf("%s: %w", tool, err)
		}
		return flattenSingleSubdir(dir)
	case javaToolTarGz:
		if err := extractJavaTarGz(archive, dir); err != nil {
			return fmt.Errorf("%s: %w", tool, err)
		}
		return flattenSingleSubdir(dir)
	default:
		return fmt.Errorf("%s: unknown archive kind", tool)
	}
}

func copyToJar(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// downloadVerified fetches url to a temp file and verifies it against a
// literal pinned sha256 — unlike toolchain.download, which fetches a
// sidecar checksum file live, these tools' release pages don't publish one
// at a predictable URL, so the checksum is pinned in javaToolAssets
// instead.
func downloadVerified(url, wantSHA256 string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	f, err := os.CreateTemp("", "codebase-analyser-java-dl-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, h), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("download %s: %w", url, copyErr)
	}
	if closeErr != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("download %s: %w", url, closeErr)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA256 {
		os.Remove(f.Name())
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, got, wantSHA256)
	}
	return f.Name(), nil
}

// safeJoinJava rejects archive entries that would write outside dest —
// mirrors toolchain.safeJoin exactly, duplicated rather than exported
// across the package boundary (same call pythonMarkers' own duplication
// comment makes for internal/detect vs internal/toolchain).
func safeJoinJava(dest, name string) (string, error) {
	target := filepath.Join(dest, filepath.FromSlash(name))
	prefix := filepath.Clean(dest) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), prefix) {
		return "", fmt.Errorf("archive entry escapes destination: %q", name)
	}
	return target, nil
}

// extractZip unpacks a .zip archive into dest — PMD and Dependency-Check
// are both distributed this way (unlike Go/Rust/the JDK, which are
// .tar.gz).
func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		target, err := safeJoinJava(dest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode()|0o600)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// extractJavaTarGz unpacks a .tar.gz archive into dest — SpotBugs is the
// one pinned Java tool shipped that way rather than as a .zip.
func extractJavaTarGz(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoinJava(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
}

// flattenSingleSubdir moves a directory's sole child up one level and
// removes the now-empty wrapper. PMD/Dependency-Check/SpotBugs archives
// each contain one top-level "toolname-version/" folder, and pinnedJavaBin
// stays version-agnostic by not needing to know that folder's exact name —
// same reasoning EnsureJDK's firstSubdir documents for the JDK archive.
func flattenSingleSubdir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}
	sub := filepath.Join(dir, entries[0].Name())
	tmp := dir + ".flatten-tmp"
	if err := os.Rename(sub, tmp); err != nil {
		return err
	}
	if err := os.Remove(dir); err != nil {
		return err
	}
	return os.Rename(tmp, dir)
}
