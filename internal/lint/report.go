package lint

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Format selects the report renderer.
type Format string

// Supported report formats.
const (
	FormatTable    Format = "table"
	FormatJSON     Format = "json"
	FormatMarkdown Format = "markdown"
)

// ParseFormat turns a flag value into a Format.
func ParseFormat(s string) (Format, error) {
	switch Format(strings.ToLower(s)) {
	case FormatTable:
		return FormatTable, nil
	case FormatJSON:
		return FormatJSON, nil
	case FormatMarkdown:
		return FormatMarkdown, nil
	}
	return "", fmt.Errorf("unknown format %q (want table, json or markdown)", s)
}

func location(f Finding) string {
	loc := fmt.Sprintf("%s:%d", f.File, f.Line)
	if f.Job != "" {
		loc += " " + f.Job
		if f.Step != nil {
			loc += fmt.Sprintf("[%d]", *f.Step)
		}
	}
	return loc
}

// Report renders findings in the requested format. scanned is the number of
// workflow files read, used for the empty-result line.
func Report(w io.Writer, findings []Finding, format Format, scanned int) error {
	switch format {
	case FormatJSON:
		return reportJSON(w, findings, scanned)
	case FormatMarkdown:
		return reportMarkdown(w, findings, scanned)
	default:
		return reportTable(w, findings, scanned)
	}
}

func reportJSON(w io.Writer, findings []Finding, scanned int) error {
	if findings == nil {
		findings = []Finding{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Scanned  int       `json:"scanned"`
		Findings []Finding `json:"findings"`
	}{scanned, findings})
}

func reportTable(w io.Writer, findings []Finding, scanned int) error {
	if len(findings) == 0 {
		_, err := fmt.Fprintf(w, "gha-timeout-guard: %d workflow(s) scanned, no findings\n", scanned)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SEVERITY\tRULE\tLOCATION\tFINDING")
	for _, f := range findings {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", f.Severity, f.Rule, location(f), f.Message)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%d finding(s) in %d workflow(s). Add:\n\n%s\n",
		len(findings), scanned, fixBlock(findings))
	return err
}

func reportMarkdown(w io.Writer, findings []Finding, scanned int) error {
	if len(findings) == 0 {
		_, err := fmt.Fprintf(w, "### gha-timeout-guard\n\n%d workflow(s) scanned, no findings.\n", scanned)
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### gha-timeout-guard\n\n%d finding(s) in %d workflow(s).\n\n", len(findings), scanned)
	b.WriteString("| Severity | Rule | Location | Finding |\n|---|---|---|---|\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "| %s | `%s` | `%s` | %s |\n", f.Severity, f.Rule, location(f), f.Message)
	}
	fmt.Fprintf(&b, "\n<details><summary>Suggested YAML</summary>\n\n```yaml\n%s```\n\n</details>\n", fixBlock(findings))
	_, err := io.WriteString(w, b.String())
	return err
}

// fixBlock collects the suggested YAML, one entry per finding that has one.
func fixBlock(findings []Finding) string {
	var b strings.Builder
	for _, f := range findings {
		if f.Fix == "" {
			continue
		}
		fmt.Fprintf(&b, "# %s:%d (%s)\n%s\n", f.File, f.Line, f.Rule, f.Fix)
	}
	return b.String()
}
