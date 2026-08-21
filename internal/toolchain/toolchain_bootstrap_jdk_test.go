package toolchain

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnsureJDKRejectsAChecksumMismatch(t *testing.T) {
	// Create a test server that serves a body and checksum that don't match
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000"))
			return
		}
		w.Write([]byte("not-a-real-archive"))
	}))
	t.Cleanup(srv.Close)

	// Temporarily inject a jdkBuildAssets entry pointing to the test server
	testURL := srv.URL + "/jdk-test.tar.gz"
	testSumURL := srv.URL + "/jdk-test.tar.gz.sha256"
	jdkBuildAssets["test-version"] = struct{ url, sumURL string }{url: testURL, sumURL: testSumURL}
	t.Cleanup(func() {
		delete(jdkBuildAssets, "test-version")
	})

	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())

	_, err := EnsureJDK("test-version")
	if err == nil {
		t.Fatal("err = nil, want a checksum-mismatch error")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("err = %v, want it to mention 'checksum'", err)
	}
}
