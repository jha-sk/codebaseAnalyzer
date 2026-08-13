package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

// testStore opens the shared test database and truncates it, so every test
// starts from an empty schema. Skips when DASHBOARD_TEST_DSN is unset, the
// same convention as the repo's existing e2e_test.go.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DASHBOARD_TEST_DSN")
	if dsn == "" {
		t.Skip("DASHBOARD_TEST_DSN not set, skipping Postgres-backed test")
	}
	s, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Reset(context.Background()); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNormalizeRemoteURL(t *testing.T) {
	const widgets = "github.com/acme/widgets"
	cases := []struct{ in, want string }{
		{"git@github.com:acme/widgets.git", widgets},
		{"https://github.com/acme/widgets.git", widgets},
		{"https://github.com/acme/widgets", widgets},
		{"ssh://git@github.com/acme/widgets.git", widgets},
		{"HTTPS://GitHub.com/Acme/Widgets/", widgets},
		{"ssh://git@github.com:22/acme/widgets.git", widgets},
		{"https://github.com:8443/acme/widgets", widgets},
		{"github.com/acme/widgets", widgets},
		{"git@github.com:acme/we@rd", "github.com/acme/we@rd"},
		{"https://github.com/acme/we@rd", "github.com/acme/we@rd"},
		{"", ""},
		{"https:///no-host/path", ""},
		{"github.com/acme/wi:dgets", "github.com/acme/wi:dgets"},
		{"git@github.com:acme/wi:dgets.git", "github.com/acme/wi:dgets"},
	}
	for _, c := range cases {
		if got := NormalizeRemoteURL(c.in); got != c.want {
			t.Errorf("NormalizeRemoteURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCreateRepoIssuesUsableToken(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	repo, token, err := s.CreateRepo(ctx, "git@github.com:acme/widgets.git")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if repo.RemoteURL != "github.com/acme/widgets" {
		t.Errorf("remote_url = %q, want normalized", repo.RemoteURL)
	}
	if len(token) < 32 {
		t.Errorf("token %q is too short to be a secret", token)
	}

	got, err := s.RepoByToken(ctx, token)
	if err != nil {
		t.Fatalf("RepoByToken: %v", err)
	}
	if got.ID != repo.ID {
		t.Errorf("RepoByToken id = %d, want %d", got.ID, repo.ID)
	}
	if _, err := s.RepoByToken(ctx, "not-a-real-token"); err != ErrNotFound {
		t.Errorf("RepoByToken(bogus) err = %v, want ErrNotFound", err)
	}
}

func TestCreateRepoRejectsDuplicate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, _, err := s.CreateRepo(ctx, "github.com/acme/widgets"); err != nil {
		t.Fatalf("first CreateRepo: %v", err)
	}
	_, _, err := s.CreateRepo(ctx, "https://github.com/acme/widgets.git")
	if err == nil {
		t.Fatal("second CreateRepo of the same normalized URL succeeded, want error")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("err = %v, want an 'already registered' message", err)
	}
}

func TestRegenerateTokenInvalidatesOld(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, old, err := s.CreateRepo(ctx, "github.com/acme/widgets")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	fresh, err := s.RegenerateToken(ctx, repo.ID)
	if err != nil {
		t.Fatalf("RegenerateToken: %v", err)
	}
	if fresh == old {
		t.Fatal("RegenerateToken returned the same token")
	}
	if _, err := s.RepoByToken(ctx, old); err != ErrNotFound {
		t.Errorf("old token still works: err = %v, want ErrNotFound", err)
	}
	if _, err := s.RepoByToken(ctx, fresh); err != nil {
		t.Errorf("new token rejected: %v", err)
	}
}

func TestListAndDeleteRepos(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, _, _ := s.CreateRepo(ctx, "github.com/acme/a")
	if _, _, err := s.CreateRepo(ctx, "github.com/acme/b"); err != nil {
		t.Fatalf("CreateRepo b: %v", err)
	}

	repos, err := s.ListRepos(ctx)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("ListRepos returned %d repos, want 2", len(repos))
	}
	if repos[0].RemoteURL != "github.com/acme/a" {
		t.Errorf("ListRepos not ordered by remote_url: got %q first", repos[0].RemoteURL)
	}

	if err := s.DeleteRepo(ctx, a.ID); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	repos, _ = s.ListRepos(ctx)
	if len(repos) != 1 || repos[0].RemoteURL != "github.com/acme/b" {
		t.Errorf("after delete, ListRepos = %+v", repos)
	}
	if err := s.DeleteRepo(ctx, a.ID); err != ErrNotFound {
		t.Errorf("DeleteRepo(missing) err = %v, want ErrNotFound", err)
	}
}
