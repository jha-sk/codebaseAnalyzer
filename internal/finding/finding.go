package finding

import "fmt"

type Category string

const (
	CategoryCorrectness Category = "correctness"
	CategoryConcurrency Category = "concurrency"
	CategorySecurity    Category = "security"
	CategoryOperational Category = "operational"
)

func ParseCategory(s string) (Category, error) {
	switch Category(s) {
	case CategoryCorrectness, CategoryConcurrency, CategorySecurity, CategoryOperational:
		return Category(s), nil
	}
	return "", fmt.Errorf("invalid category %q (want correctness|concurrency|security|operational)", s)
}

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

var severityRank = map[Severity]int{
	SeverityLow:      1,
	SeverityMedium:   2,
	SeverityHigh:     3,
	SeverityCritical: 4,
}

func ParseSeverity(s string) (Severity, error) {
	if _, ok := severityRank[Severity(s)]; ok {
		return Severity(s), nil
	}
	return "", fmt.Errorf("invalid severity %q (want critical|high|medium|low)", s)
}

// MeetsThreshold reports whether sev is at or above threshold.
func MeetsThreshold(sev, threshold Severity) bool {
	return severityRank[sev] >= severityRank[threshold]
}

type Finding struct {
	File     string
	Line     int
	Tool     string
	RuleID   string
	Category Category
	Severity Severity
	Message  string
}

type ExplainedFinding struct {
	Finding
	Explanation string
	FixPattern  string
}

func WithoutExplanation(findings []Finding) []ExplainedFinding {
	out := make([]ExplainedFinding, len(findings))
	for i, f := range findings {
		out[i] = ExplainedFinding{Finding: f}
	}
	return out
}
