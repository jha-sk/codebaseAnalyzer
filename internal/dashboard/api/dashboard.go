package api

import (
	"errors"
	"net/http"

	"codebase-analyser/internal/dashboard/store"
)

// defaultHistoryLimit is how many runs the trend chart plots, and the window
// "current" and "previous" are drawn from. Fixed, not caller-tunable: a
// smaller window would make runs[len(runs)-2] the second-to-last element of
// the *limited* slice rather than the branch's actual previous run, silently
// corrupting deltas/new/fixed on any request that asked for a shorter window.
const defaultHistoryLimit = 30

// topFileCount is how many rows the "top offending files" view shows.
const topFileCount = 10

type historyPoint struct {
	RunID     int64          `json:"run_id"`
	CommitSHA string         `json:"commit_sha"`
	PushedAt  string         `json:"pushed_at"`
	Counts    map[string]int `json:"counts"`
	Health    int            `json:"health"`
}

type currentRun struct {
	Run        store.Run       `json:"run"`
	Health     int             `json:"health"`
	Deltas     map[string]int  `json:"deltas"`
	New        int             `json:"new"`
	Fixed      int             `json:"fixed"`
	Categories map[string]int  `json:"categories"`
	TopFiles   []FileCount     `json:"top_files"`
	Findings   []store.Finding `json:"findings"`
}

type dashboardResponse struct {
	Repo     store.Repo     `json:"repo"`
	Branch   string         `json:"branch"`
	Branches []store.Branch `json:"branches"`
	History  []historyPoint `json:"history"`
	Current  *currentRun    `json:"current"`
}

type runResponse struct {
	Run      store.Run       `json:"run"`
	Findings []store.Finding `json:"findings"`
}

func (s *server) registerReadRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/repos/{id}/dashboard", s.admin(s.dashboard))
	mux.HandleFunc("GET /api/runs/{id}", s.admin(s.runDetail))
}

// dashboard assembles every view on the page in one response. The frontend is
// then a pure renderer: it never has to decide which run is current, which
// branch to show, or how to derive a number.
func (s *server) dashboard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	repo, err := s.st.RepoByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "repo not found")
		return
	}
	if err != nil {
		serverError(w, "get repo", err)
		return
	}

	branches, err := s.st.Branches(ctx, repo.ID)
	if err != nil {
		serverError(w, "list branches", err)
		return
	}

	branch := r.URL.Query().Get("branch")
	if branch == "" {
		branch = PickDefaultBranch(branches)
	}
	resp := dashboardResponse{Repo: repo, Branch: branch, Branches: branches, History: []historyPoint{}}
	if branch == "" { // repo registered but nothing pushed yet
		writeJSON(w, http.StatusOK, resp)
		return
	}

	runs, err := s.st.RunsForBranch(ctx, repo.ID, branch, defaultHistoryLimit)
	if err != nil {
		serverError(w, "list runs", err)
		return
	}
	if len(runs) == 0 {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// History carries a health score per point so the trend chart and the
	// health view are reading the same series.
	for i, run := range runs {
		var prev map[string]int
		if i > 0 {
			prev = runs[i-1].Counts
		}
		resp.History = append(resp.History, historyPoint{
			RunID:     run.ID,
			CommitSHA: run.CommitSHA,
			PushedAt:  run.PushedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Counts:    run.Counts,
			Health:    HealthScore(run.Counts, prev),
		})
	}

	current := runs[len(runs)-1]
	findings, err := s.st.FindingsForRun(ctx, current.ID)
	if err != nil {
		serverError(w, "list findings", err)
		return
	}

	// "Since last run" compares the two most recent runs on this branch. With
	// only one run everything is new and nothing is fixed, which Diff already
	// yields for a nil previous.
	var prevCounts map[string]int
	var prevFindings []store.Finding
	if len(runs) > 1 {
		previous := runs[len(runs)-2]
		prevCounts = previous.Counts
		if prevFindings, err = s.st.FindingsForRun(ctx, previous.ID); err != nil {
			serverError(w, "list previous findings", err)
			return
		}
	}
	added, fixed := Diff(prevFindings, findings)

	resp.Current = &currentRun{
		Run:        current,
		Health:     HealthScore(current.Counts, prevCounts),
		Deltas:     deltas(current.Counts, prevCounts),
		New:        added,
		Fixed:      fixed,
		Categories: CategoryCounts(findings),
		TopFiles:   TopFiles(findings, topFileCount),
		Findings:   findings,
	}
	writeJSON(w, http.StatusOK, resp)
}

// deltas is the per-severity change the summary cards show. A first run has
// no previous, and reports every count as unchanged rather than as a jump
// from zero, which would read as a regression on a brand-new branch.
func deltas(cur, prev map[string]int) map[string]int {
	out := map[string]int{}
	for sev := range severityWeight {
		out[sev] = 0
		if prev != nil {
			out[sev] = cur[sev] - prev[sev]
		}
	}
	return out
}

func (s *server) runDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, err := s.st.RunByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		serverError(w, "get run", err)
		return
	}
	findings, err := s.st.FindingsForRun(r.Context(), run.ID)
	if err != nil {
		serverError(w, "list findings", err)
		return
	}
	writeJSON(w, http.StatusOK, runResponse{Run: run, Findings: findings})
}
