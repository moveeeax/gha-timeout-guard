// Command gha-timeout-guard lints GitHub Actions workflows for the
// reliability settings that are missing rather than wrong.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/moveeeax/gha-timeout-guard/internal/lint"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

type repeatable []string

func (r *repeatable) String() string { return strings.Join(*r, ",") }

func (r *repeatable) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*r = append(*r, part)
		}
	}
	return nil
}

const (
	exitOK       = 0
	exitFindings = 1
	exitError    = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("gha-timeout-guard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dir         = fs.String("dir", ".", "repository root or workflows directory to scan")
		maxTimeout  = fs.Int("max-timeout", 60, "report an explicit timeout-minutes above this ceiling (0 disables)")
		defTimeout  = fs.Int("suggest-timeout", 15, "value used in suggested timeout-minutes lines")
		minSeverity = fs.String("min-severity", "warning", "lowest severity to report: warning|error")
		failOn      = fs.String("fail-on", "warning", "exit 1 on findings at or above: warning|error|never")
		format      = fs.String("format", "table", "output format: table|json|markdown")
		showVersion = fs.Bool("version", false, "print version and exit")
		ignore      repeatable
	)
	fs.Var(&ignore, "ignore", "rule id to suppress, repeatable or comma-separated")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "gha-timeout-guard — lint GitHub Actions workflows for missing timeouts,\n"+
			"missing concurrency groups and swallowed failures.\n\nUsage:\n  gha-timeout-guard [flags]\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(stderr, "\nRules: %s\n", strings.Join(lint.Rules, ", "))
		fmt.Fprintf(stderr, "Exit codes: 0 clean, 1 findings at or above --fail-on, 2 error.\n")
	}
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *showVersion {
		fmt.Fprintf(stdout, "gha-timeout-guard %s\n", version)
		return exitOK
	}

	minSev, err := lint.ParseSeverity(*minSeverity)
	if err != nil {
		fmt.Fprintf(stderr, "gha-timeout-guard: %v\n", err)
		return exitError
	}
	outFormat, err := lint.ParseFormat(*format)
	if err != nil {
		fmt.Fprintf(stderr, "gha-timeout-guard: %v\n", err)
		return exitError
	}
	failSev, failNever, err := parseFailOn(*failOn)
	if err != nil {
		fmt.Fprintf(stderr, "gha-timeout-guard: %v\n", err)
		return exitError
	}
	for _, r := range ignore {
		if !knownRule(r) {
			fmt.Fprintf(stderr, "gha-timeout-guard: unknown rule %q (known: %s)\n", r, strings.Join(lint.Rules, ", "))
			return exitError
		}
	}

	files, err := lint.Discover(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "gha-timeout-guard: %v\n", err)
		return exitError
	}
	if len(files) == 0 {
		fmt.Fprintf(stderr, "gha-timeout-guard: no workflow files under %s\n", *dir)
		return exitError
	}

	cfg := lint.Config{MaxTimeout: *maxTimeout, DefaultTimeout: *defTimeout}
	var findings []lint.Finding
	for _, f := range files {
		wf, err := lint.ParseFile(f)
		if err != nil {
			fmt.Fprintf(stderr, "gha-timeout-guard: %v\n", err)
			return exitError
		}
		findings = append(findings, lint.Analyze(wf, cfg)...)
	}

	findings = lint.Filter(findings, ignore, minSev)
	if err := lint.Report(stdout, findings, outFormat, len(files)); err != nil {
		fmt.Fprintf(stderr, "gha-timeout-guard: %v\n", err)
		return exitError
	}

	if failNever {
		return exitOK
	}
	for _, f := range findings {
		if f.Level() >= failSev {
			return exitFindings
		}
	}
	return exitOK
}

func parseFailOn(v string) (lint.Severity, bool, error) {
	if strings.EqualFold(v, "never") {
		return 0, true, nil
	}
	s, err := lint.ParseSeverity(v)
	if err != nil {
		return 0, false, fmt.Errorf("unknown --fail-on %q (want warning, error or never)", v)
	}
	return s, false, nil
}

func knownRule(r string) bool {
	for _, k := range lint.Rules {
		if k == r {
			return true
		}
	}
	return false
}
