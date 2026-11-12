package lint

import (
	"fmt"
	"regexp"
	"strings"
)

// Severity orders findings; the zero value is the lowest level reported.
type Severity int

// Severity levels, ordered.
const (
	SevWarning Severity = iota
	SevError
)

func (s Severity) String() string {
	if s == SevError {
		return "error"
	}
	return "warning"
}

// ParseSeverity turns a flag value into a Severity.
func ParseSeverity(s string) (Severity, error) {
	switch strings.ToLower(s) {
	case "warning", "warn":
		return SevWarning, nil
	case "error":
		return SevError, nil
	}
	return 0, fmt.Errorf("unknown severity %q (want warning or error)", s)
}

// Rule identifiers, usable with --ignore.
const (
	RuleJobTimeout   = "job-timeout"
	RuleMaxTimeout   = "max-timeout"
	RuleConcurrency  = "concurrency"
	RuleJobContinue  = "job-continue-on-error"
	RuleStepContinue = "step-continue-on-error"
	RuleStepTimeout  = "step-timeout"
)

// Rules lists every rule id the linter can emit.
var Rules = []string{
	RuleJobTimeout,
	RuleMaxTimeout,
	RuleConcurrency,
	RuleJobContinue,
	RuleStepContinue,
	RuleStepTimeout,
}

// Finding is one rule violation at one place in one file.
type Finding struct {
	File     string `json:"file"`
	Job      string `json:"job,omitempty"`
	Step     *int   `json:"step,omitempty"`
	Line     int    `json:"line"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"`

	sev Severity
}

// Level returns the parsed severity of the finding.
func (f Finding) Level() Severity { return f.sev }

// Config holds the knobs the rules read.
type Config struct {
	// MaxTimeout is the ceiling, in minutes, above which an explicit
	// timeout-minutes is reported. Zero disables the ceiling.
	MaxTimeout int
	// DefaultTimeout is the value used in suggested timeout-minutes lines.
	DefaultTimeout int
}

// netCommands are the step commands that reach the network and therefore hang
// instead of failing when the far end stops answering.
var netCommands = []string{"curl", "wget", "docker pull", "apt-get"}

func indent(col int) string {
	if col < 1 {
		col = 1
	}
	return strings.Repeat(" ", col-1)
}

func stepPtr(i int) *int { return &i }

func (c Config) defaultTimeout() int {
	if c.DefaultTimeout > 0 {
		return c.DefaultTimeout
	}
	return 15
}

// Analyze runs every rule against one parsed workflow.
func Analyze(wf *Workflow, cfg Config) []Finding {
	var out []Finding
	add := func(f Finding) {
		f.Severity = f.sev.String()
		f.File = wf.Path
		out = append(out, f)
	}

	out = append(out, concurrencyFindings(wf)...)

	for _, j := range wf.Jobs {
		switch {
		case j.Reusable:
			// GitHub rejects timeout-minutes on a job that calls a reusable
			// workflow; the timeout has to live inside the called workflow,
			// so flagging it here would be unfixable advice.
		case !j.HasTimeout:
			add(Finding{
				Job: j.ID, Line: j.Line, Rule: RuleJobTimeout, sev: SevError,
				Message: fmt.Sprintf("job %q has no timeout-minutes and can hang for the 6h runner default", j.ID),
				Fix:     indent(j.PropCol) + fmt.Sprintf("timeout-minutes: %d", cfg.defaultTimeout()),
			})
		case cfg.MaxTimeout > 0 && j.Timeout > cfg.MaxTimeout:
			add(Finding{
				Job: j.ID, Line: j.TimeoutLine, Rule: RuleMaxTimeout, sev: SevWarning,
				Message: fmt.Sprintf("job %q sets timeout-minutes: %d, above the %d minute ceiling", j.ID, j.Timeout, cfg.MaxTimeout),
				Fix:     indent(j.PropCol) + fmt.Sprintf("timeout-minutes: %d", cfg.MaxTimeout),
			})
		}

		if j.ContinueOnError {
			add(Finding{
				Job: j.ID, Line: j.ContinueLine, Rule: RuleJobContinue, sev: SevError,
				Message: fmt.Sprintf("job %q sets continue-on-error: true, so the whole job reports success when it fails", j.ID),
				Fix:     indent(j.PropCol) + "continue-on-error: false",
			})
		}

		for _, s := range j.Steps {
			if s.ContinueOnError {
				add(Finding{
					Job: j.ID, Step: stepPtr(s.Index), Line: s.ContinueLine, Rule: RuleStepContinue, sev: SevWarning,
					Message: fmt.Sprintf("step %q in job %q swallows its own failure with continue-on-error: true", s.Label(), j.ID),
					Fix:     indent(s.PropCol) + "continue-on-error: false",
				})
			}
			if cmd, ok := networkCommand(s.Run); ok && !s.HasTimeout {
				add(Finding{
					Job: j.ID, Step: stepPtr(s.Index), Line: s.Line, Rule: RuleStepTimeout, sev: SevWarning,
					Message: fmt.Sprintf("step %q in job %q runs %s with no step timeout-minutes", s.Label(), j.ID, cmd),
					Fix:     indent(s.PropCol) + "timeout-minutes: 5",
				})
			}
		}
	}
	return out
}

// deployPattern matches job text that writes to something outside the runner.
// It is the same pattern gha-concurrency-audit classifies deploys with, so the
// two tools agree on which workflows must queue instead of cancel. Bare verbs
// are matched as words; apply and up need their tool in front.
var deployPattern = regexp.MustCompile(`(?i)(\b(deploy|deployment|rollout|release|publish)\b|` +
	`\b(terraform|tofu|terragrunt)\s+apply\b|` +
	`\bkubectl\s+(apply|rollout|set\s+image)\b|` +
	`\bhelm\s+(upgrade|install)\b|` +
	`\bkustomize\s+build\b.*\bkubectl\b|` +
	`\bpulumi\s+up\b|` +
	`\bserverless\s+deploy\b|` +
	`\baws\s+(deploy|s3\s+sync|cloudformation\s+deploy)\b|` +
	`\b(npm|cargo|twine|gem)\s+publish\b|` +
	`\bdocker\s+push\b)`)

// IsDeploy reports whether any job declares an environment: or runs a command
// that changes state outside the runner. Cancelling such a run mid-way leaves
// a half-applied deploy, so it must queue rather than cancel.
func (w *Workflow) IsDeploy() bool {
	for _, j := range w.Jobs {
		if j.IsDeploy() {
			return true
		}
	}
	return false
}

// IsDeploy reports whether this job declares an environment: or runs a
// command matching deployPattern.
func (j Job) IsDeploy() bool {
	return j.Environment || deployPattern.MatchString(j.text)
}

func concurrencyFindings(wf *Workflow) []Finding {
	stacks := wf.HasEvent("push") || wf.HasEvent("pull_request") || wf.HasEvent("pull_request_target")
	if !stacks {
		// A schedule- or dispatch-only workflow does not stack on pushes, so
		// a concurrency group would only cancel unrelated runs.
		return nil
	}
	if wf.IsDeploy() {
		return deployConcurrencyFindings(wf)
	}
	mk := func(line int, rule, msg, fix string, sev Severity) Finding {
		return Finding{File: wf.Path, Line: line, Rule: rule, Severity: sev.String(), Message: msg, Fix: fix, sev: sev}
	}
	switch {
	case !wf.Conc.Present:
		return []Finding{mk(1, RuleConcurrency,
			"workflow triggers on push or pull_request with no concurrency group, so every push runs a full duplicate pipeline",
			concurrencyBlock(wf.RootPropCol, true), SevWarning)}
	case !wf.Conc.Cancel:
		return []Finding{mk(wf.Conc.Line, RuleConcurrency,
			"concurrency group has no cancel-in-progress: true, so superseded runs keep burning runner minutes",
			indent(wf.RootPropCol)+"  cancel-in-progress: true", SevWarning)}
	}
	return nil
}

// deployConcurrencyFindings applies the opposite advice to a deploy: it needs
// a group so two pushes do not apply at once, and that group must queue. A
// group on the deploy job itself is as good as one on the workflow. A group on
// a build or test job is left alone: cancelling it only stops an older run
// before it reaches the deploy.
func deployConcurrencyFindings(wf *Workflow) []Finding {
	var out []Finding
	cancelling := func(c Concurrency, job string, line, col int) {
		if !c.Cancel && !c.CancelExpr {
			return
		}
		what := "sets cancel-in-progress: true"
		if c.CancelExpr {
			what = "sets cancel-in-progress to an expression that can evaluate true"
		}
		subject := "deploy workflow"
		if job != "" {
			subject = fmt.Sprintf("job %q in a deploy workflow", job)
		}
		out = append(out, Finding{
			File: wf.Path, Job: job, Line: line, Rule: RuleConcurrency,
			Severity: SevWarning.String(), sev: SevWarning,
			Message: subject + " " + what + ", so a newer push cancels a deploy mid-apply and leaves it half-applied",
			Fix:     indent(col) + "  cancel-in-progress: false",
		})
	}

	guarded := wf.Conc.Present
	if wf.Conc.Present {
		cancelling(wf.Conc, "", wf.Conc.Line, wf.RootPropCol)
	}
	for _, j := range wf.Jobs {
		if j.Conc.Present && j.IsDeploy() {
			guarded = true
			cancelling(j.Conc, j.ID, j.Conc.Line, j.PropCol)
		}
	}
	if !guarded {
		out = append(out, Finding{
			File: wf.Path, Line: 1, Rule: RuleConcurrency,
			Severity: SevWarning.String(), sev: SevWarning,
			Message: "deploy workflow triggers on push or pull_request with no concurrency group, so two runs can apply at once and the slower one wins",
			Fix:     concurrencyBlock(wf.RootPropCol, false),
		})
	}
	return out
}

// concurrencyBlock renders the workflow-level block to paste. Deploys get
// cancel-in-progress: false so a newer run waits for the current one.
func concurrencyBlock(col int, cancel bool) string {
	return indent(col) + "concurrency:\n" +
		indent(col) + "  group: ${{ github.workflow }}-${{ github.ref }}\n" +
		indent(col) + fmt.Sprintf("  cancel-in-progress: %t", cancel)
}

// networkCommand reports the first network-reaching command found in a run
// script. Comment lines are skipped so a mention of curl in a comment does not
// produce a finding.
func networkCommand(run string) (string, bool) {
	if run == "" {
		return "", false
	}
	for _, raw := range strings.Split(run, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, cmd := range netCommands {
			if containsCommand(line, cmd) {
				return cmd, true
			}
		}
	}
	return "", false
}

// containsCommand looks for cmd as a standalone token so that, for example,
// "wgetlog" or "./curl-helper.sh" do not match.
func containsCommand(line, cmd string) bool {
	for i := 0; i+len(cmd) <= len(line); i++ {
		if line[i:i+len(cmd)] != cmd {
			continue
		}
		if i > 0 && isWordByte(line[i-1]) {
			continue
		}
		if j := i + len(cmd); j < len(line) && isWordByte(line[j]) {
			continue
		}
		return true
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '-' || b == '_' || b == '.' || b == '/' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// Filter drops ignored rules and findings below minimum severity.
func Filter(findings []Finding, ignore []string, min Severity) []Finding {
	skip := make(map[string]bool, len(ignore))
	for _, r := range ignore {
		skip[strings.TrimSpace(r)] = true
	}
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if skip[f.Rule] || f.sev < min {
			continue
		}
		out = append(out, f)
	}
	return out
}
