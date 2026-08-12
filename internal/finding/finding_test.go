package finding

import "testing"

func TestParseSeverity(t *testing.T) {
	if _, err := ParseSeverity("bogus"); err == nil {
		t.Fatal("expected error for invalid severity")
	}
	sev, err := ParseSeverity("high")
	if err != nil || sev != SeverityHigh {
		t.Fatalf("got %v, %v; want SeverityHigh, nil", sev, err)
	}
}

func TestParseCategory(t *testing.T) {
	if _, err := ParseCategory("bogus"); err == nil {
		t.Fatal("expected error for invalid category")
	}
	cat, err := ParseCategory("security")
	if err != nil || cat != CategorySecurity {
		t.Fatalf("got %v, %v; want CategorySecurity, nil", cat, err)
	}
}

func TestMeetsThreshold(t *testing.T) {
	cases := []struct {
		sev, threshold Severity
		want           bool
	}{
		{SeverityCritical, SeverityHigh, true},
		{SeverityHigh, SeverityHigh, true},
		{SeverityMedium, SeverityHigh, false},
		{SeverityLow, SeverityLow, true},
	}
	for _, c := range cases {
		if got := MeetsThreshold(c.sev, c.threshold); got != c.want {
			t.Errorf("MeetsThreshold(%v, %v) = %v, want %v", c.sev, c.threshold, got, c.want)
		}
	}
}

func TestWithoutExplanation(t *testing.T) {
	in := []Finding{{Tool: "gosec", RuleID: "G101"}}
	out := WithoutExplanation(in)
	if len(out) != 1 || out[0].Tool != "gosec" || out[0].Explanation != "" {
		t.Fatalf("got %+v", out)
	}
}
