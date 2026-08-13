// Package store is the dashboard's Postgres layer. It deliberately holds no
// derived logic: every number the UI shows is computed from these rows by
// pure functions in internal/dashboard/api, so they stay testable without a
// database.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schema string

// ErrNotFound is returned for a lookup that matched no row. Callers map it to
// 401 (bad ingest token) or 404 (missing repo/run) as appropriate.
var ErrNotFound = errors.New("not found")

// ErrRepoExists is returned when a repo with the same normalized remote URL is
// already registered. Callers map it to 409.
var ErrRepoExists = errors.New("repo already registered")

type Store struct{ db *sql.DB }

type Repo struct {
	ID           int64     `json:"id"`
	RemoteURL    string    `json:"remote_url"`
	RegisteredAt time.Time `json:"registered_at"`
}

// Open connects and applies the schema. The schema is idempotent DDL rather
// than a migration tool: v1 only ever adds tables, and a self-hosted binary
// that sets itself up on first boot is the whole point of the deployment story.
// ponytail: no migration framework. Add one the first time a column has to
// change shape under live data.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Reset truncates every table. It exists for tests; production never calls it.
func (s *Store) Reset(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `TRUNCATE repos RESTART IDENTITY CASCADE`)
	return err
}

// NormalizeRemoteURL reduces the many spellings of one git remote to a single
// identity: scheme, credentials, port, .git suffix and trailing slash are all
// stripped and the result is lowercased, so git@github.com:acme/w.git,
// https://github.com/acme/w and ssh://git@github.com:22/acme/w all key the
// same repo row.
func NormalizeRemoteURL(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return ""
	}
	// scp-style (git@host:org/repo) is not a URL and net/url cannot parse it,
	// so rewrite it into one first. Credentials are stripped from the host
	// portion only - an "@" inside a path segment is part of the path.
	if !strings.Contains(s, "://") {
		// A colon separates host from path only when it comes before the first
		// slash; deeper in the string it belongs to the path, which may
		// legitimately contain one. Splitting on the first colon anywhere
		// silently rewrote such a path segment as an extra directory level.
		host, path := s, ""
		sep := strings.Index(s, "/")
		if colon := strings.Index(s, ":"); colon >= 0 && (sep < 0 || colon < sep) {
			sep = colon
		}
		if sep >= 0 {
			host, path = s[:sep], s[sep+1:]
		}
		// Credentials live only in the host portion; an "@" in a path segment
		// is part of the path. LastIndex, so a userinfo containing an encoded
		// "@" still yields the real host.
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		s = "ssh://" + host + "/" + path
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	// Hostname() drops the port: one remote spelled with and without an
	// explicit port must not become two repos.
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if path == "" {
		return strings.ToLower(u.Hostname())
	}
	return strings.ToLower(u.Hostname() + "/" + path)
}

// newToken returns a 256-bit URL-safe secret and its storage hash. The token
// is generated here rather than chosen by a human, so it has full entropy and
// a plain SHA-256 is the right store: bcrypt/argon2 defend against brute-force
// on low-entropy human passwords, which this can never be.
func newToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateRepo registers a repo and returns its ingest token in plaintext. That
// return value is the only time the token exists outside a hash - the caller
// must show it to the admin once and then drop it.
func (s *Store) CreateRepo(ctx context.Context, remoteURL string) (Repo, string, error) {
	normalized := NormalizeRemoteURL(remoteURL)
	if normalized == "" {
		return Repo{}, "", errors.New("remote_url is required")
	}
	token, hash, err := newToken()
	if err != nil {
		return Repo{}, "", err
	}
	repo := Repo{RemoteURL: normalized}
	err = s.db.QueryRowContext(ctx,
		`INSERT INTO repos (remote_url, ingest_token_hash) VALUES ($1, $2)
		 ON CONFLICT (remote_url) DO NOTHING
		 RETURNING id, registered_at`,
		normalized, hash).Scan(&repo.ID, &repo.RegisteredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, "", fmt.Errorf("%w: %s", ErrRepoExists, normalized)
	}
	if err != nil {
		return Repo{}, "", fmt.Errorf("insert repo: %w", err)
	}
	return repo, token, nil
}

func (s *Store) ListRepos(ctx context.Context) ([]Repo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, remote_url, registered_at FROM repos ORDER BY remote_url`)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()
	repos := []Repo{}
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.RemoteURL, &r.RegisteredAt); err != nil {
			return nil, fmt.Errorf("scan repo: %w", err)
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

func (s *Store) RepoByID(ctx context.Context, id int64) (Repo, error) {
	var r Repo
	err := s.db.QueryRowContext(ctx,
		`SELECT id, remote_url, registered_at FROM repos WHERE id = $1`, id).
		Scan(&r.ID, &r.RemoteURL, &r.RegisteredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	if err != nil {
		return Repo{}, fmt.Errorf("get repo: %w", err)
	}
	return r, nil
}

// RepoByToken resolves an ingest token to its repo. The lookup is by hash, so
// no comparison against a stored secret happens in Go at all.
func (s *Store) RepoByToken(ctx context.Context, token string) (Repo, error) {
	if token == "" {
		return Repo{}, ErrNotFound
	}
	var r Repo
	err := s.db.QueryRowContext(ctx,
		`SELECT id, remote_url, registered_at FROM repos WHERE ingest_token_hash = $1`,
		hashToken(token)).Scan(&r.ID, &r.RemoteURL, &r.RegisteredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	if err != nil {
		return Repo{}, fmt.Errorf("get repo by token: %w", err)
	}
	return r, nil
}

func (s *Store) RegenerateToken(ctx context.Context, id int64) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE repos SET ingest_token_hash = $1 WHERE id = $2`, hash, id)
	if err != nil {
		return "", fmt.Errorf("regenerate token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return token, nil
}

// DeleteRepo removes the repo and, by ON DELETE CASCADE, all of its runs and
// findings. This is the spec's "revoke a repo's token" in its strongest form.
func (s *Store) DeleteRepo(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM repos WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete repo: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
