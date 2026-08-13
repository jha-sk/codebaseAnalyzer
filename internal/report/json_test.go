package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestRenderJSON(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{File: "bad.go", Line: 6, Tool: "gosec", RuleID: "G101",
			Category: finding.CategorySecurity, Severity: finding.SeverityCritical, Message: "hardcoded credential"},
			Explanation: "why"},
	}
	var buf bytes.Buffer
	if err := RenderJSON(&buf, findings, nil); err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Summary  map[string]int `json:"summary"`
		Findings []struct {
			File        string `json:"file"`
			Line        int    `json:"line"`
			RuleID      string `json:"ruleID"`
			Explanation string `json:"explanation"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Summary["critical"] != 1 {
		t.Errorf("summary.critical = %d, want 1", parsed.Summary["critical"])
	}
	if len(parsed.Findings) != 1 || parsed.Findings[0].RuleID != "G101" || parsed.Findings[0].Explanation != "why" {
		t.Errorf("findings = %+v", parsed.Findings)
	}
}

// TestRenderJSONEmptyFindings ensures a nil/empty findings slice marshals
// "findings" as [] rather than null, so downstream JSON consumers (e.g. CI
// tooling) never have to special-case a missing array.
func TestRenderJSONEmptyFindings(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, nil, nil); err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(buf.Bytes(), []byte(`"findings": null`)) {
		t.Errorf("findings must not serialize as null: %s", buf.String())
	}

	var parsed struct {
		Summary  map[string]int    `json:"summary"`
		Findings []json.RawMessage `json:"findings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Findings == nil {
		t.Errorf("findings should unmarshal as an empty (non-nil) slice, got nil")
	}
	if len(parsed.Findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(parsed.Findings))
	}
	for _, sev := range []string{"critical", "high", "medium", "low"} {
		if parsed.Summary[sev] != 0 {
			t.Errorf("summary.%s = %d, want 0", sev, parsed.Summary[sev])
		}
	}
}

// TestRenderJSONEmptyExplanation ensures a finding with an empty Explanation
// (the LLM-failure fallback path) still round-trips as an empty string, not
// an omitted field.
func TestRenderJSONEmptyExplanation(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{File: "x.go", Line: 1, Tool: "t", RuleID: "R",
			Category: finding.CategoryCorrectness, Severity: finding.SeverityLow, Message: "msg"},
			Explanation: ""},
	}
	var buf bytes.Buffer
	if err := RenderJSON(&buf, findings, nil); err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Findings []struct {
			Explanation string `json:"explanation"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(parsed.Findings))
	}
	if parsed.Findings[0].Explanation != "" {
		t.Errorf("explanation = %q, want empty string", parsed.Findings[0].Explanation)
	}
}

// TestRenderJSONDoesNotEscapeHTML ensures messages containing angle brackets
// or ampersands (routine in Go concurrency-linter output, e.g. "<-chan")
// render as literal text in the raw JSON bytes rather than the
// HTML-escaped unicode form (< etc.) that encoding/json produces by
// default — valid either way, but the escaped form is much harder to read.
func TestRenderJSONDoesNotEscapeHTML(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{File: "chan.go", Line: 1, Tool: "t", RuleID: "R",
			Category: finding.CategoryConcurrency, Severity: finding.SeverityLow,
			Message: "send on <-chan int & receive race"}},
	}
	var buf bytes.Buffer
	if err := RenderJSON(&buf, findings, nil); err != nil {
		t.Fatal(err)
	}
	raw := buf.String()

	if !strings.Contains(raw, "<-chan int & receive") {
		t.Errorf("expected literal <-chan and & in raw output, got:\n%s", raw)
	}
	// encoding/json's default HTML-escaping turns <, >, & into six-character
	// backslash-u unicode escapes. Build the marker from a rune to sidestep
	// any editor/tool auto-normalization of escape sequences in source text.
	backslash := string(rune(0x5C))
	for _, codepoint := range []string{"003c", "003e", "0026"} {
		marker := backslash + "u" + codepoint
		if strings.Contains(raw, marker) {
			t.Errorf("output was HTML-escaped (found %s), got:\n%s", marker, raw)
		}
	}
}

// TestRenderJSONFixPattern is Critical 3's JSON-side regression test:
// fixPattern must be duplicated per finding, exactly like explanation, so
// downstream CI tooling reading the flat findings array doesn't need to
// understand the (tool, ruleID) grouping the LLM step batches on.
func TestRenderJSONFixPattern(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{File: "bad.go", Line: 6, Tool: "gosec", RuleID: "G101",
			Category: finding.CategorySecurity, Severity: finding.SeverityCritical, Message: "hardcoded credential"},
			Explanation: "why", FixPattern: "load from a secrets manager instead"},
	}
	var buf bytes.Buffer
	if err := RenderJSON(&buf, findings, nil); err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Findings []struct {
			FixPattern string `json:"fixPattern"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Findings) != 1 || parsed.Findings[0].FixPattern != "load from a secrets manager instead" {
		t.Errorf("findings = %+v", parsed.Findings)
	}
}

// TestRenderJSONEmptyFixPattern ensures an empty FixPattern (the --no-llm or
// failed-group fallback) round-trips as an empty string, not an omitted
// field - matching the existing Explanation contract.
func TestRenderJSONEmptyFixPattern(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{File: "x.go", Line: 1, Tool: "t", RuleID: "R",
			Category: finding.CategoryCorrectness, Severity: finding.SeverityLow, Message: "msg"}},
	}
	var buf bytes.Buffer
	if err := RenderJSON(&buf, findings, nil); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Findings []struct {
			FixPattern string `json:"fixPattern"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Findings) != 1 || parsed.Findings[0].FixPattern != "" {
		t.Errorf("findings = %+v", parsed.Findings)
	}
}

// TestRenderJSON_skippedToolsMakeIncompleteCoverageMachineReadable is
// Critical 2's JSON-side regression test: a consumer reading only stdout
// JSON (the spec's explicit CI-tooling rationale) must be able to see
// incomplete coverage from the document itself, not just a stderr note or
// the process exit code.
func TestRenderJSON_skippedToolsMakeIncompleteCoverageMachineReadable(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, nil, []SkippedTool{
		{Tool: "govulncheck", Path: "/repo", Reason: "not found in $PATH"},
	}); err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Incomplete   bool `json:"incomplete"`
		SkippedTools []struct {
			Tool   string `json:"tool"`
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"skippedTools"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if !parsed.Incomplete {
		t.Error("incomplete = false, want true when a tool was skipped")
	}
	if len(parsed.SkippedTools) != 1 || parsed.SkippedTools[0].Tool != "govulncheck" || parsed.SkippedTools[0].Path != "/repo" {
		t.Errorf("skippedTools = %+v", parsed.SkippedTools)
	}
}

// TestRenderJSON_noSkippedToolsIsComplete ensures the clean-run path
// reports incomplete=false and an empty (not null) skippedTools array.
func TestRenderJSON_noSkippedToolsIsComplete(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, nil, nil); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte(`"skippedTools": null`)) {
		t.Errorf("skippedTools must not serialize as null: %s", buf.String())
	}
	var parsed struct {
		Incomplete   bool          `json:"incomplete"`
		SkippedTools []SkippedTool `json:"skippedTools"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Incomplete {
		t.Error("incomplete = true, want false when nothing was skipped")
	}
	if parsed.SkippedTools == nil || len(parsed.SkippedTools) != 0 {
		t.Errorf("skippedTools = %+v, want empty non-nil slice", parsed.SkippedTools)
	}
}
