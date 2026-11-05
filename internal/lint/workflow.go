// Package lint parses GitHub Actions workflows and reports reliability
// settings that are missing rather than wrong: job timeouts, concurrency
// groups, and swallowed failures.
package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Step is one entry of a job's steps: list.
type Step struct {
	Index           int
	Name            string
	Run             string
	Uses            string
	Line            int
	PropCol         int
	HasTimeout      bool
	ContinueOnError bool
	ContinueLine    int
}

// Label returns a human-friendly identifier for a step.
func (s Step) Label() string {
	switch {
	case s.Name != "":
		return s.Name
	case s.Uses != "":
		return s.Uses
	default:
		return fmt.Sprintf("step %d", s.Index)
	}
}

// Job is one entry of the workflow's jobs: mapping.
type Job struct {
	ID              string
	Line            int
	PropCol         int
	HasTimeout      bool
	Timeout         int
	TimeoutLine     int
	Reusable        bool
	ContinueOnError bool
	ContinueLine    int
	Steps           []Step
}

// Workflow is a parsed workflow file.
type Workflow struct {
	Path        string
	Name        string
	Events      []string
	HasConc     bool
	ConcLine    int
	Cancel      bool
	RootPropCol int
	Jobs        []Job
}

// HasEvent reports whether the workflow is triggered by the named event.
func (w *Workflow) HasEvent(name string) bool {
	for _, e := range w.Events {
		if e == name {
			return true
		}
	}
	return false
}

func mapGet(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func mapKey(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i]
		}
	}
	return nil
}

// propCol returns the column the children of a mapping are written at, so a
// suggested line can be indented exactly like its neighbours.
func propCol(n *yaml.Node, fallback int) int {
	if n != nil && n.Kind == yaml.MappingNode && len(n.Content) > 0 {
		return n.Content[0].Column
	}
	return fallback
}

// isTrue reports whether a scalar is the literal boolean true. An expression
// such as ${{ github.event_name == 'push' }} is deliberately not literal true.
func isTrue(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && strings.EqualFold(n.Value, "true")
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

// events flattens the on: value, which may be a scalar, a sequence or a
// mapping. The key is matched by its literal text because YAML 1.1 resolves a
// bare on into the boolean true, so the tag cannot be trusted here.
func events(n *yaml.Node) []string {
	var out []string
	switch {
	case n == nil:
	case n.Kind == yaml.ScalarNode:
		out = append(out, n.Value)
	case n.Kind == yaml.SequenceNode:
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
	case n.Kind == yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			out = append(out, n.Content[i].Value)
		}
	}
	return out
}

func onNode(root *yaml.Node) *yaml.Node {
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		if k.Value == "on" || k.Value == "true" && k.Tag == "!!bool" {
			return root.Content[i+1]
		}
	}
	return nil
}

// Parse reads a workflow from bytes. path is only used for reporting.
func Parse(path string, data []byte) (*Workflow, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s: empty document", path)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: top level is not a mapping", path)
	}

	wf := &Workflow{
		Path:        path,
		Name:        scalar(mapGet(root, "name")),
		Events:      events(onNode(root)),
		RootPropCol: propCol(root, 1),
	}

	if conc := mapGet(root, "concurrency"); conc != nil {
		wf.HasConc = true
		wf.ConcLine = mapKey(root, "concurrency").Line
		if conc.Kind == yaml.MappingNode {
			wf.Cancel = isTrue(mapGet(conc, "cancel-in-progress"))
		}
	}

	jobs := mapGet(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: no jobs mapping", path)
	}
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		k, v := jobs.Content[i], jobs.Content[i+1]
		if v.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s: job %q is not a mapping", path, k.Value)
		}
		j := Job{
			ID:      k.Value,
			Line:    k.Line,
			PropCol: propCol(v, k.Column+2),
		}
		j.Reusable = mapGet(v, "uses") != nil
		if t := mapGet(v, "timeout-minutes"); t != nil {
			j.HasTimeout = true
			j.TimeoutLine = mapKey(v, "timeout-minutes").Line
			j.Timeout, _ = strconv.Atoi(t.Value)
		}
		if c := mapGet(v, "continue-on-error"); isTrue(c) {
			j.ContinueOnError = true
			j.ContinueLine = mapKey(v, "continue-on-error").Line
		}
		if steps := mapGet(v, "steps"); steps != nil && steps.Kind == yaml.SequenceNode {
			for si, sn := range steps.Content {
				if sn.Kind != yaml.MappingNode {
					continue
				}
				s := Step{
					Index:   si,
					Name:    scalar(mapGet(sn, "name")),
					Run:     scalar(mapGet(sn, "run")),
					Uses:    scalar(mapGet(sn, "uses")),
					Line:    sn.Line,
					PropCol: propCol(sn, j.PropCol+4),
				}
				s.HasTimeout = mapGet(sn, "timeout-minutes") != nil
				if c := mapGet(sn, "continue-on-error"); isTrue(c) {
					s.ContinueOnError = true
					s.ContinueLine = mapKey(sn, "continue-on-error").Line
				}
				j.Steps = append(j.Steps, s)
			}
		}
		wf.Jobs = append(wf.Jobs, j)
	}
	return wf, nil
}

// ParseFile reads and parses a workflow from disk.
func ParseFile(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, data)
}

// Discover resolves dir to a list of workflow files. A repository root is
// accepted directly: if dir contains .github/workflows, that directory is
// scanned instead, so --dir . and --dir .github/workflows both work.
func Discover(dir string) ([]string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{dir}, nil
	}
	target := dir
	if sub := filepath.Join(dir, ".github", "workflows"); dirExists(sub) {
		target = sub
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".yml", ".yaml":
			out = append(out, filepath.Join(target, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
