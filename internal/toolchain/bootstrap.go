package toolchain

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"codebase-analyser/internal/cache"
)

// Download bases are read from the environment so tests can point them at a
// local server. They are env vars rather than package variables because a
// test that spawns the server as a subprocess needs them to cross that
// boundary.
func goDownloadBase() string {
	if base := os.Getenv("CODEBASE_ANALYSER_GO_DL"); base != "" {
		return base
	}
	return "https://go.dev/dl/"
}

func rustupDownloadBase() string {
	if base := os.Getenv("CODEBASE_ANALYSER_RUSTUP_DL"); base != "" {
		return base
	}
	return "https://static.rust-lang.org/rustup/dist/"
}

// toolchainsDir is where downloaded toolchains live, alongside the analysis
// cache and under the same CODEBASE_ANALYSER_CACHE override.
func toolchainsDir() (string, error) {
	root, err := cache.Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "toolchains"), nil
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// EnsureGo returns the GOROOT of a Go toolchain we manage ourselves,
// downloading and extracting it on first use. The user's own Go install is
// never touched.
//
// Windows is not supported here: the official Go distribution for Windows is
// a .zip, and this package's extractor only reads .tar.gz. Rather than ship
// an untested zip-extraction path, EnsureGo fails clearly on Windows so the
// caller falls back to "install Go yourself" instead of silently corrupting
// an extraction.
func EnsureGo(version string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("automatic Go bootstrap is not supported on Windows yet; install Go and re-run")
	}

	dir, err := toolchainsDir()
	if err != nil {
		return "", err
	}
	// The official archive contains a single top-level "go" directory, so
	// the extracted GOROOT sits one level inside the versioned root.
	root := filepath.Join(dir, "go", version)
	goroot := filepath.Join(root, "go")
	if isExecutable(filepath.Join(goroot, "bin", exeName("go"))) {
		return goroot, nil
	}

	unlock, err := lock(dir, "go-"+version)
	if err != nil {
		return "", err
	}
	defer unlock()
	// Another process may have finished while we waited for the lock.
	if isExecutable(filepath.Join(goroot, "bin", exeName("go"))) {
		return goroot, nil
	}

	url := fmt.Sprintf("%sgo%s.%s-%s.tar.gz", goDownloadBase(), version, runtime.GOOS, runtime.GOARCH)

	// stderr, never stdout: this can run inside an MCP session where stdout
	// carries the JSON-RPC stream.
	fmt.Fprintf(os.Stderr, "codebase-analyser: downloading Go %s for %s/%s (first run only)...\n",
		version, runtime.GOOS, runtime.GOARCH)

	archive, err := download(url, url+".sha256")
	if err != nil {
		return "", err
	}
	defer os.Remove(archive)

	// Extract to a staging directory and rename into place, so an
	// interrupted run never leaves a half-extracted toolchain that the
	// cache-hit check above would then accept.
	staging := root + ".staging"
	os.RemoveAll(staging)
	if err := extractTarGz(archive, staging); err != nil {
		os.RemoveAll(staging)
		return "", err
	}
	os.RemoveAll(root)
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(staging, root); err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "codebase-analyser: Go %s ready\n", version)
	return goroot, nil
}

// EnsureRustup returns a CARGO_HOME/RUSTUP_HOME we populate with rustup-init.
// The user's own ~/.cargo and ~/.rustup are never touched.
func EnsureRustup() (string, error) {
	dir, err := toolchainsDir()
	if err != nil {
		return "", err
	}
	home := filepath.Join(dir, "rust")
	if isExecutable(filepath.Join(home, "bin", exeName("cargo"))) {
		return home, nil
	}

	unlock, err := lock(dir, "rust")
	if err != nil {
		return "", err
	}
	defer unlock()
	if isExecutable(filepath.Join(home, "bin", exeName("cargo"))) {
		return home, nil
	}

	triple, err := rustTargetTriple()
	if err != nil {
		return "", err
	}
	url := rustupDownloadBase() + triple + "/" + exeName("rustup-init")
	fmt.Fprintln(os.Stderr, "codebase-analyser: installing an isolated Rust toolchain (first run only)...")

	init, err := download(url, url+".sha256")
	if err != nil {
		return "", err
	}
	defer os.Remove(init)
	if err := os.Chmod(init, 0o755); err != nil {
		return "", err
	}

	cmd := exec.Command(init, "-y", "--no-modify-path", "--profile", "minimal",
		"--default-toolchain", "stable", "-c", "clippy")
	cmd.Env = append(os.Environ(), "RUSTUP_HOME="+home, "CARGO_HOME="+home)
	cmd.Stdout = os.Stderr // rustup's progress output must not reach stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("rustup-init: %w", err)
	}
	fmt.Fprintln(os.Stderr, "codebase-analyser: Rust toolchain ready")
	return home, nil
}

func rustTargetTriple() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "x86_64-unknown-linux-gnu", nil
	case "linux/arm64":
		return "aarch64-unknown-linux-gnu", nil
	case "darwin/amd64":
		return "x86_64-apple-darwin", nil
	case "darwin/arm64":
		return "aarch64-apple-darwin", nil
	case "windows/amd64":
		return "x86_64-pc-windows-msvc", nil
	}
	return "", fmt.Errorf("no rustup build for %s/%s", runtime.GOOS, runtime.GOARCH)
}

// download fetches url to a temp file, verifying it against the checksum
// served at sumURL. Nothing is ever extracted or executed before the hash
// matches. url and sumURL are the only inputs: this helper knows nothing
// about Go or Rust, so any future caller (e.g. a Node toolchain) can reuse
// it verbatim.
func download(url, sumURL string) (string, error) {
	want, err := fetchChecksum(sumURL)
	if err != nil {
		return "", err
	}

	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	f, err := os.CreateTemp("", "codebase-analyser-dl-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, h), resp.Body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("download %s: %w", url, errOr(copyErr, closeErr))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(f.Name())
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, got, want)
	}
	return f.Name(), nil
}

func errOr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func fetchChecksum(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum at %s", url)
	}
	return strings.ToLower(fields[0]), nil
}

// safeJoin rejects archive entries that would write outside dest. An archive
// is untrusted input until proven otherwise, and "../.." in an entry name is
// the classic way out of an extraction directory. Language-agnostic: takes
// only a destination directory and an entry name.
func safeJoin(dest, name string) (string, error) {
	target := filepath.Join(dest, filepath.FromSlash(name))
	prefix := filepath.Clean(dest) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), prefix) {
		return "", fmt.Errorf("archive entry escapes destination: %q", name)
	}
	return target, nil
}

// extractTarGz unpacks a .tar.gz archive into dest. It knows nothing about
// what the archive contains: Go's distribution today, a Node tarball
// tomorrow. Symlinks and device entries are skipped outright, since a
// symlink is the easiest way to write outside dest even when the entry name
// itself looks safe.
func extractTarGz(src, dest string) error {
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
		target, err := safeJoin(dest, hdr.Name)
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
		default:
			// Symlinks and devices are skipped: the Go archive has none that
			// matter, and a symlink is the easiest way to write outside dest.
		}
	}
}

// lock serializes bootstraps across processes, so two analyses starting at
// once cannot extract into the same directory. A lock left behind by a killed
// process is reclaimed once it is clearly stale. Keyed by name only, so it
// serves any future toolchain (go-1.26.5, rust, node-20) without change.
func lock(dir, name string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".lock")
	deadline := time.Now().Add(10 * time.Minute)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 15*time.Minute {
			os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for %s", path)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
