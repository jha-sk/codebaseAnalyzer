package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ToolStatus mirrors one orchestrator.ToolResult as the CLI pushes it: which
// tool ran, and if it did not, why. Stored as JSONB on the run rather than in
// its own table - it is written once and read once, purely for display.
type ToolStatus struct {
	Name    string `json:"name"`
	Skipped bool   `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

// Finding is one row of the CLI's JSON report. Field names match that
// report's JSON keys so the ingest handler can decode straight into it.
type Finding struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Tool        string `json:"tool"`
	RuleID      string `json:"ruleID"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
	Explanation string `json:"explanation"`
}

type Run struct {
	ID        int64          `json:"id"`
	RepoID    int64          `json:"repo_id"`
	Branch    string         `json:"branch"`
	CommitSHA string         `json:"commit_sha"`
	PushedAt  time.Time      `json:"pushed_at"`
	Counts    map[string]int `json:"counts"`
	Tools     []ToolStatus   `json:"tools"`
}

type Branch struct {
	Name      string         `json:"name"`
	LastRunAt time.Time      `json:"last_run_at"`
	CommitSHA string         `json:"commit_sha"`
	RunID     int64          `json:"run_id"`
	Counts    map[string]int `json:"counts"`
}

// severities is the fixed key set for every Counts map, so the UI can index
// all four without nil checks even when a severity has no findings.
var severities = [4]string{"critical", "high", "medium", "low"}

// severityRank and validCategories are the fixed sets a finding's Severity
// and Category must belong to. SaveRun rejects anything outside them: an
// unknown severity would be persisted in findings but counted nowhere in
// runs.critical/high/medium/low, letting the two silently disagree.
var severityRank = map[string]bool{"critical": true, "high": true, "medium": true, "low": true}

var validCategories = map[string]bool{"correctness": true, "concurrency": true, "security": true, "operational": true}

// ErrInvalidFinding is returned when a finding carries a severity or category
// outside the fixed sets. Callers map it to a 400: it means the pushed report
// is malformed, not that the server failed.
var ErrInvalidFinding = errors.New("invalid finding")

func countBySeverity(findings []Finding) map[string]int {
	counts := map[string]int{}
	for _, s := range severities {
		counts[s] = 0
	}
	for _, f := range findings {
		if _, known := counts[f.Severity]; known {
			counts[f.Severity]++
		}
	}
	return counts
}

// SaveRun writes one CI push. It is an upsert on (repo_id, commit_sha): a
// retry of the same commit replaces that run's counts, tool statuses and
// findings wholesale rather than adding a second row. The whole thing runs in
// one transaction, so a failure mid-write can never leave a run whose stored
// counts disagree with its stored findings. Every finding's severity and
// category is validated against the fixed sets before anything is written,
// for the same reason: an unrecognised value would be stored but counted
// nowhere. Note that the upsert key is (repo_id, commit_sha) only, not
// branch: re-pushing the same commit under a different branch name moves the
// run's branch column rather than creating a second row, so it disappears
// from the original branch's history. That is intended, not a bug.
func (s *Store) SaveRun(ctx context.Context, repoID int64, branch, commitSHA string, tools []ToolStatus, findings []Finding) (int64, error) {
	if branch == "" || commitSHA == "" {
		return 0, errors.New("branch and commit are required")
	}
	for i, f := range findings {
		if _, ok := severityRank[f.Severity]; !ok {
			return 0, fmt.Errorf("%w: findings[%d] severity %q (want critical|high|medium|low)", ErrInvalidFinding, i, f.Severity)
		}
		if !validCategories[f.Category] {
			return 0, fmt.Errorf("%w: findings[%d] category %q (want correctness|concurrency|security|operational)", ErrInvalidFinding, i, f.Category)
		}
	}
	if tools == nil {
		tools = []ToolStatus{}
	}
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return 0, fmt.Errorf("encode tool statuses: %w", err)
	}
	counts := countBySeverity(findings)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op once Commit has succeeded

	var runID int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO runs (repo_id, branch, commit_sha, critical, high, medium, low, tools)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (repo_id, commit_sha) DO UPDATE SET
		   branch = EXCLUDED.branch, pushed_at = now(),
		   critical = EXCLUDED.critical, high = EXCLUDED.high,
		   medium = EXCLUDED.medium, low = EXCLUDED.low, tools = EXCLUDED.tools
		 RETURNING id`,
		repoID, branch, commitSHA,
		counts["critical"], counts["high"], counts["medium"], counts["low"],
		toolsJSON).Scan(&runID)
	if err != nil {
		return 0, fmt.Errorf("upsert run: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM findings WHERE run_id = $1`, runID); err != nil {
		return 0, fmt.Errorf("clear previous findings: %w", err)
	}
	// ponytail: one INSERT per finding inside the transaction. A run carries
	// tens to low hundreds of findings, so this is milliseconds; switch to
	// pgx.CopyFrom if a run ever pushes tens of thousands.
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO findings (run_id, file, line, tool, rule_id, category, severity, message, explanation)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`)
	if err != nil {
		return 0, fmt.Errorf("prepare finding insert: %w", err)
	}
	defer stmt.Close()
	for _, f := range findings {
		if _, err := stmt.ExecContext(ctx, runID, f.File, f.Line, f.Tool, f.RuleID,
			f.Category, f.Severity, f.Message, f.Explanation); err != nil {
			return 0, fmt.Errorf("insert finding %s:%d: %w", f.File, f.Line, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit run: %w", err)
	}
	return runID, nil
}

func scanRun(rows interface{ Scan(...any) error }) (Run, error) {
	var (
		r          Run
		toolsJSON  []byte
		c, h, m, l int
	)
	if err := rows.Scan(&r.ID, &r.RepoID, &r.Branch, &r.CommitSHA, &r.PushedAt, &c, &h, &m, &l, &toolsJSON); err != nil {
		return Run{}, err
	}
	r.Counts = map[string]int{"critical": c, "high": h, "medium": m, "low": l}
	r.Tools = []ToolStatus{}
	if err := json.Unmarshal(toolsJSON, &r.Tools); err != nil {
		return Run{}, fmt.Errorf("decode tool statuses: %w", err)
	}
	return r, nil
}

const runColumns = `id, repo_id, branch, commit_sha, pushed_at, critical, high, medium, low, tools`

func (s *Store) RunByID(ctx context.Context, runID int64) (Run, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, runID)
	r, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("get run: %w", err)
	}
	return r, nil
}

// RunsForBranch returns up to limit runs, keeping the NEWEST ones but
// returning them oldest-first so the trend chart can render them left to
// right without reversing.
func (s *Store) RunsForBranch(ctx context.Context, repoID int64, branch string, limit int) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT * FROM (
		   SELECT `+runColumns+` FROM runs
		   WHERE repo_id = $1 AND branch = $2
		   ORDER BY pushed_at DESC, id DESC LIMIT $3
		 ) newest ORDER BY pushed_at ASC, id ASC`,
		repoID, branch, limit)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// Branches summarises every branch that has ever pushed, by its latest run,
// most recently active first. DISTINCT ON is Postgres's direct way to say
// "one row per branch, the newest" without a self-join.
func (s *Store) Branches(ctx context.Context, repoID int64) ([]Branch, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT branch, pushed_at, commit_sha, id, critical, high, medium, low FROM (
		   SELECT DISTINCT ON (branch) branch, pushed_at, commit_sha, id, critical, high, medium, low
		   FROM runs WHERE repo_id = $1
		   ORDER BY branch, pushed_at DESC, id DESC
		 ) latest ORDER BY pushed_at DESC, id DESC`, repoID)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	defer rows.Close()
	branches := []Branch{}
	for rows.Next() {
		var (
			b          Branch
			c, h, m, l int
		)
		if err := rows.Scan(&b.Name, &b.LastRunAt, &b.CommitSHA, &b.RunID, &c, &h, &m, &l); err != nil {
			return nil, fmt.Errorf("scan branch: %w", err)
		}
		b.Counts = map[string]int{"critical": c, "high": h, "medium": m, "low": l}
		branches = append(branches, b)
	}
	return branches, rows.Err()
}

// FindingsForRun returns a run's findings in a stable order (file, then line),
// so two renders of the same run never disagree.
func (s *Store) FindingsForRun(ctx context.Context, runID int64) ([]Finding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT file, line, tool, rule_id, category, severity, message, explanation
		 FROM findings WHERE run_id = $1 ORDER BY file, line, rule_id, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list findings: %w", err)
	}
	defer rows.Close()
	findings := []Finding{}
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.File, &f.Line, &f.Tool, &f.RuleID, &f.Category,
			&f.Severity, &f.Message, &f.Explanation); err != nil {
			return nil, fmt.Errorf("scan finding: %w", err)
		}
		findings = append(findings, f)
	}
	return findings, rows.Err()
}
