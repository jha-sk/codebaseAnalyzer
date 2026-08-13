package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"

	"codebase-analyser/internal/dashboard/store"
)

// maxBodyBytes caps an ingest push. A run's report is measured in tens of
// kilobytes; this is the trust-boundary guard that stops an ingest token from
// being a memory-exhaustion lever.
const maxBodyBytes = 32 << 20 // 32 MiB

// maxBranchLen and maxCommitLen bound branch and commit as stored: both are
// TEXT columns with unlimited retention, so without a check here an ingest
// token could grow them without limit up to maxBodyBytes.
const (
	maxBranchLen = 255
	maxCommitLen = 64
)

type server struct {
	st         *store.Store
	adminToken string
}

// New builds the whole HTTP surface: the JSON API plus the embedded SPA on /.
// assets must contain index.html at its root.
func New(st *store.Store, adminToken string, assets fs.FS) http.Handler {
	s := &server{st: st, adminToken: adminToken}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/admin/repos", s.admin(s.createRepo))
	mux.HandleFunc("GET /api/admin/repos", s.admin(s.listRepos))
	mux.HandleFunc("POST /api/admin/repos/{id}/token", s.admin(s.regenerateToken))
	mux.HandleFunc("DELETE /api/admin/repos/{id}", s.admin(s.deleteRepo))
	mux.HandleFunc("POST /api/runs", s.ingest)
	s.registerReadRoutes(mux) // Task 5

	mux.Handle("GET /", http.FileServerFS(assets))
	return mux
}

// admin gates a handler behind the deploy-time admin token. The comparison is
// constant-time: a timing oracle on a long-lived shared secret is worth the
// one-line defence.
func (s *server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if s.adminToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.adminToken)) != 1 {
			httpError(w, http.StatusUnauthorized, "invalid admin token")
			return
		}
		h(w, r)
	}
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // findings routinely contain "<-chan"
	if err := enc.Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// pathID reads a {id} path value, replying 404 for anything unparseable -
// a non-numeric id can only ever be a route that matches nothing.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusNotFound, "not found")
		return 0, false
	}
	return id, true
}

func (s *server) createRepo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RemoteURL string `json:"remote_url"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if strings.TrimSpace(req.RemoteURL) == "" {
		httpError(w, http.StatusBadRequest, "remote_url is required")
		return
	}
	if store.NormalizeRemoteURL(req.RemoteURL) == "" {
		httpError(w, http.StatusBadRequest, "remote_url is not a usable git remote")
		return
	}
	repo, token, err := s.st.CreateRepo(r.Context(), req.RemoteURL)
	if err != nil {
		if errors.Is(err, store.ErrRepoExists) {
			httpError(w, http.StatusConflict, err.Error())
			return
		}
		serverError(w, "create repo", err)
		return
	}
	// The only time the token is ever returned. It is not stored in plaintext,
	// so an admin who loses it must regenerate rather than look it up.
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": repo.ID, "remote_url": repo.RemoteURL, "token": token,
	})
}

func (s *server) listRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.st.ListRepos(r.Context())
	if err != nil {
		serverError(w, "list repos", err)
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

func (s *server) regenerateToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	token, err := s.st.RegenerateToken(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "repo not found")
		return
	}
	if err != nil {
		serverError(w, "regenerate token", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (s *server) deleteRepo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.st.DeleteRepo(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "repo not found")
		return
	}
	if err != nil {
		serverError(w, "delete repo", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ingestRequest is the CLI's push envelope: git metadata the CLI detects,
// the tool statuses from the orchestrator, and the unmodified JSON report.
type ingestRequest struct {
	Branch string             `json:"branch"`
	Commit string             `json:"commit"`
	Tools  []store.ToolStatus `json:"tools"`
	Report struct {
		Findings []store.Finding `json:"findings"`
	} `json:"report"`
}

// ingest is authenticated by the per-repo ingest token alone: the token IS
// the repo identity, so there is no repo id in the path that could disagree
// with it and no way for one repo's token to write another repo's history.
func (s *server) ingest(w http.ResponseWriter, r *http.Request) {
	repo, err := s.st.RepoByToken(r.Context(), bearer(r))
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusUnauthorized, "invalid ingest token")
		return
	}
	if err != nil {
		serverError(w, "resolve ingest token", err)
		return
	}

	var req ingestRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if req.Branch == "" || req.Commit == "" {
		httpError(w, http.StatusBadRequest, "branch and commit are required")
		return
	}
	// Both land in unlimited-retention TEXT columns; without a bound here the
	// only limit is the 32 MiB body cap. The numbers are generous relative to
	// real git refs (a full SHA-1 or SHA-256 hex commit is at most 64 chars)
	// and branch names (git itself limits ref components well under 255).
	if len(req.Branch) > maxBranchLen || len(req.Commit) > maxCommitLen {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("branch must be <= %d chars and commit <= %d chars", maxBranchLen, maxCommitLen))
		return
	}

	// The pushed summary is deliberately ignored; SaveRun recounts from the
	// findings so stored counts can never contradict stored findings.
	runID, err := s.st.SaveRun(r.Context(), repo.ID, req.Branch, req.Commit, req.Tools, req.Report.Findings)
	if err != nil {
		if errors.Is(err, store.ErrInvalidFinding) {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		serverError(w, "save run", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return err
	}
	return nil
}

// serverError logs the real cause and returns a generic message, so a SQL
// error never travels to a client.
func serverError(w http.ResponseWriter, what string, err error) {
	log.Printf("%s: %v", what, err)
	httpError(w, http.StatusInternalServerError, what+" failed")
}
