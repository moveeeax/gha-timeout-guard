package lint

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func analyzeFixture(t *testing.T, name string) []Finding {
	t.Helper()
	wf, err := ParseFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return Analyze(wf, Config{MaxTimeout: 60, DefaultTimeout: 15})
}

func rules(findings []Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Rule)
	}
	return out
}

func only(t *testing.T, findings []Finding, rule string) []Finding {
	t.Helper()
	var out []Finding
	for _, f := range findings {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}

func TestCleanWorkflowHasNoFindings(t *testing.T) {
	if got := analyzeFixture(t, "clean.yml"); len(got) != 0 {
		t.Fatalf("expected no findings, got %v", rules(got))
	}
}

func TestMissingJobTimeoutReportedOncePerJob(t *testing.T) {
	got := only(t, analyzeFixture(t, "no-timeout.yml"), RuleJobTimeout)
	if len(got) != 1 {
		t.Fatalf("expected 1 job-timeout finding, got %d (%v)", len(got), rules(got))
	}
	f := got[0]
	if f.Job != "build" {
		t.Errorf("job = %q, want build", f.Job)
	}
	if f.Severity != "error" {
		t.Errorf("severity = %q, want error", f.Severity)
	}
	if f.Line != 9 {
		t.Errorf("line = %d, want 9 (the job key)", f.Line)
	}
	if f.Fix != "    timeout-minutes: 15" {
		t.Errorf("fix = %q, want the job-indented timeout line", f.Fix)
	}
	if !strings.Contains(f.File, "no-timeout.yml") {
		t.Errorf("file = %q", f.File)
	}
}

func TestPushWorkflowWithoutConcurrencyIsFlagged(t *testing.T) {
	got := only(t, analyzeFixture(t, "stacking.yml"), RuleConcurrency)
	if len(got) != 1 {
		t.Fatalf("expected 1 concurrency finding, got %d", len(got))
	}
	if !strings.Contains(got[0].Fix, "cancel-in-progress: true") {
		t.Errorf("fix should suggest a full concurrency block, got %q", got[0].Fix)
	}
}

func TestConcurrencyWithoutCancelIsFlagged(t *testing.T) {
	got := only(t, analyzeFixture(t, "no-cancel.yml"), RuleConcurrency)
	if len(got) != 1 {
		t.Fatalf("expected 1 concurrency finding, got %d", len(got))
	}
	if got[0].Line != 4 {
		t.Errorf("line = %d, want 4 (the concurrency key)", got[0].Line)
	}
}

func TestScheduleOnlyWorkflowIsExemptFromConcurrency(t *testing.T) {
	for _, f := range analyzeFixture(t, "cron.yml") {
		t.Fatalf("cron workflow should be clean, got %s: %s", f.Rule, f.Message)
	}
}

func TestContinueOnErrorSeverityDiffersByLevel(t *testing.T) {
	got := analyzeFixture(t, "continue-on-error.yml")
	job := only(t, got, RuleJobContinue)
	step := only(t, got, RuleStepContinue)
	if len(job) != 1 || job[0].Severity != "error" {
		t.Fatalf("job continue-on-error should be one error finding, got %+v", job)
	}
	if len(step) != 1 || step[0].Severity != "warning" {
		t.Fatalf("step continue-on-error should be one warning finding, got %+v", step)
	}
	if step[0].Step == nil || *step[0].Step != 1 {
		t.Errorf("step index = %v, want 1", step[0].Step)
	}
}

func TestNetworkStepWithoutTimeout(t *testing.T) {
	got := only(t, analyzeFixture(t, "net-step.yml"), RuleStepTimeout)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 step-timeout finding, got %d: %+v", len(got), got)
	}
	if got[0].Step == nil || *got[0].Step != 0 {
		t.Errorf("step index = %v, want 0 (the curl step)", got[0].Step)
	}
	if !strings.Contains(got[0].Message, "curl") {
		t.Errorf("message should name the command, got %q", got[0].Message)
	}
}

func TestReusableWorkflowJobIsNotFlaggedForTimeout(t *testing.T) {
	if got := analyzeFixture(t, "reusable.yml"); len(got) != 0 {
		t.Fatalf("reusable-call job should be exempt, got %v", rules(got))
	}
}

func TestTimeoutAboveCeiling(t *testing.T) {
	got := only(t, analyzeFixture(t, "big-timeout.yml"), RuleMaxTimeout)
	if len(got) != 1 {
		t.Fatalf("expected 1 max-timeout finding, got %d", len(got))
	}
	if got[0].Severity != "warning" {
		t.Errorf("severity = %q, want warning", got[0].Severity)
	}
	if !strings.Contains(got[0].Message, "360") {
		t.Errorf("message should quote the declared timeout, got %q", got[0].Message)
	}
}

func TestMaxTimeoutDisabledByZero(t *testing.T) {
	wf, err := ParseFile(filepath.Join("..", "..", "testdata", "big-timeout.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := Analyze(wf, Config{MaxTimeout: 0}); len(got) != 0 {
		t.Fatalf("ceiling 0 should disable the rule, got %v", rules(got))
	}
}

func TestParseErrorOnBrokenYAML(t *testing.T) {
	if _, err := ParseFile(filepath.Join("..", "..", "testdata", "broken.yml")); err == nil {
		t.Fatal("expected a parse error for broken.yml")
	}
}

func TestOnKeyIsReadDespiteYAMLBooleanResolution(t *testing.T) {
	wf, err := ParseFile(filepath.Join("..", "..", "testdata", "stacking.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !wf.HasEvent("push") || !wf.HasEvent("pull_request") {
		t.Fatalf("events = %v, want push and pull_request", wf.Events)
	}
}

func TestFilterIgnoresOneRuleAndKeepsTheRest(t *testing.T) {
	got := analyzeFixture(t, "continue-on-error.yml")
	kept := Filter(got, []string{RuleJobContinue}, SevWarning)
	for _, f := range kept {
		if f.Rule == RuleJobContinue {
			t.Fatalf("%s should have been ignored", f.Rule)
		}
	}
	if len(kept) != len(got)-1 {
		t.Fatalf("kept %d of %d findings, want exactly one dropped", len(kept), len(got))
	}
}

func TestFilterMinSeverity(t *testing.T) {
	got := analyzeFixture(t, "continue-on-error.yml")
	kept := Filter(got, nil, SevError)
	if len(kept) == 0 {
		t.Fatal("expected at least one error finding")
	}
	for _, f := range kept {
		if f.Severity != "error" {
			t.Fatalf("min-severity error kept a %s finding", f.Severity)
		}
	}
}

func TestDiscoverPrefersGithubWorkflows(t *testing.T) {
	files, err := Discover(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("expected the repository's own workflows to be discovered")
	}
	for _, f := range files {
		if !strings.Contains(filepath.ToSlash(f), ".github/workflows/") {
			t.Fatalf("discovered %s outside .github/workflows", f)
		}
	}
}

func TestDiscoverWorkflowsDirectoryDirectly(t *testing.T) {
	files, err := Discover(filepath.Join("..", "..", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 9 {
		t.Fatalf("expected the fixtures to be discovered, got %d", len(files))
	}
}

func TestOwnWorkflowsAreClean(t *testing.T) {
	files, err := Discover(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		wf, err := ParseFile(path)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, f := range Analyze(wf, Config{MaxTimeout: 60, DefaultTimeout: 15}) {
			t.Errorf("%s: %s: %s", path, f.Rule, f.Message)
		}
	}
}

func TestReportJSONRoundTrips(t *testing.T) {
	got := analyzeFixture(t, "continue-on-error.yml")
	var buf bytes.Buffer
	if err := Report(&buf, got, FormatJSON, 1); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Scanned  int       `json:"scanned"`
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if out.Scanned != 1 || len(out.Findings) != len(got) {
		t.Fatalf("scanned=%d findings=%d, want 1 and %d", out.Scanned, len(out.Findings), len(got))
	}
}

func TestReportMarkdownAndTableMentionTheRule(t *testing.T) {
	got := analyzeFixture(t, "no-timeout.yml")
	for _, format := range []Format{FormatTable, FormatMarkdown} {
		var buf bytes.Buffer
		if err := Report(&buf, got, format, 1); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), RuleJobTimeout) {
			t.Errorf("%s output does not mention %s:\n%s", format, RuleJobTimeout, buf.String())
		}
		if !strings.Contains(buf.String(), "timeout-minutes: 15") {
			t.Errorf("%s output does not carry the suggested line:\n%s", format, buf.String())
		}
	}
}

func TestEmptyReportIsNotAnError(t *testing.T) {
	for _, format := range []Format{FormatTable, FormatMarkdown, FormatJSON} {
		var buf bytes.Buffer
		if err := Report(&buf, nil, format, 3); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if buf.Len() == 0 {
			t.Errorf("%s produced no output", format)
		}
	}
}

func TestParseSeverityAndFormatReject(t *testing.T) {
	if _, err := ParseSeverity("critical"); err == nil {
		t.Error("expected an error for an unknown severity")
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Error("expected an error for an unknown format")
	}
}

func TestContainsCommandWordBoundaries(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"curl -fsSL https://x", true},
		{"sudo apt-get update", true},
		{"./curl-helper.sh", false},
		{"echo wgetlog", false},
		{"docker pull alpine", true},
		{"docker push alpine", false},
	}
	for _, c := range cases {
		if _, got := networkCommand(c.line); got != c.want {
			t.Errorf("networkCommand(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}
