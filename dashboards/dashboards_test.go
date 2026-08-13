// Package dashboards_test validates the dashboards this exporter ships against
// the metrics it actually emits. It runs entirely offline.
package dashboards_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql/parser"

	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/protect"
	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/unas"
)

// promqlParser is the parser instance used throughout this file. This
// version of the module dropped the package-level parser.ParseExpr free
// function in favor of a Parser built via NewParser; there is no per-call
// state, so one shared instance is safe to reuse across tests.
var promqlParser = parser.NewParser(parser.Options{})

// emitted builds the authoritative set of metric names and their label keys by
// running the mappers over fixtures. Derived from the code, never from a
// hand-maintained list, so it cannot rot.
func emitted(t *testing.T) (names map[string]bool, labels map[string]map[string]bool) {
	t.Helper()
	names = map[string]bool{}
	labels = map[string]map[string]bool{}

	record := func(name string, ls map[string]string) {
		names[name] = true
		if labels[name] == nil {
			labels[name] = map[string]bool{}
		}
		for k := range ls {
			labels[name][k] = true
		}
	}

	for _, s := range unas.Samples(unas.FixtureStorage(), "fallback", time.Now()) {
		record(s.Name, s.Labels)
	}
	for _, s := range protect.Samples(protect.FixtureBootstrap(t), "fallback") {
		record(s.Name, s.Labels)
	}
	for _, s := range unas.RemoteBackupSamples(unas.FixtureRemoteBackupTasks(), time.Now()) {
		record(s.Name, s.Labels)
	}

	// The collector wrapper's own series are emitted outside the mappers.
	// unas_remote_backup is a third namespace, not a typo: that collector runs
	// under its own so its scrape_collector gauges are not label-identical to
	// the storage collector's. See unas.RemoteBackupSource.Namespace.
	for _, ns := range []string{"unas", "protect", "unas_remote_backup"} {
		record(ns+"_scrape_collector_success", nil)
		record(ns+"_scrape_collector_duration_seconds", nil)
	}
	return names, labels
}

// dashboard is the subset of the Grafana schema this test reads.
type dashboard struct {
	Inputs     json.RawMessage `json:"__inputs"`
	Requires   json.RawMessage `json:"__requires"`
	Templating struct {
		List []struct {
			Name       string          `json:"name"`
			Type       string          `json:"type"`
			Query      json.RawMessage `json:"query"`
			Datasource json.RawMessage `json:"datasource"`
		} `json:"list"`
	} `json:"templating"`
	Panels []panel `json:"panels"`
}

type panel struct {
	Title   string  `json:"title"`
	Panels  []panel `json:"panels"` // rows nest their children
	Targets []struct {
		Expr string `json:"expr"`
	} `json:"targets"`
}

func flatten(ps []panel) []panel {
	var out []panel
	for _, p := range ps {
		out = append(out, p)
		out = append(out, flatten(p.Panels)...)
	}
	return out
}

func load(t *testing.T, path string) *dashboard {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var d dashboard
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return &d
}

func dashboardFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no dashboards found: %v", err)
	}
	return files
}

// ourNamespace reports whether a metric name belongs to this exporter. Names
// outside it (up, recording rules) are legitimately referenced and skipped.
func ourNamespace(name string) bool {
	return strings.HasPrefix(name, "unas_") || strings.HasPrefix(name, "protect_")
}

func TestEveryQueryReferencesAnEmittedMetric(t *testing.T) {
	names, _ := emitted(t)

	var queriesChecked int
	for _, f := range dashboardFiles(t) {
		d := load(t, f)

		exprs := map[string]string{} // expr -> where it came from
		for _, p := range flatten(d.Panels) {
			for _, tgt := range p.Targets {
				if tgt.Expr != "" {
					exprs[tgt.Expr] = "panel " + p.Title
				}
			}
		}
		// Template-variable queries matter as much as panel queries: a
		// label_values() over a dead metric leaves the variable unresolvable and
		// EVERY panel filtering on it renders empty — a total blackout, not one
		// bad panel.
		for _, v := range d.Templating.List {
			if q := templateQuery(v.Query); q != "" {
				exprs[q] = "template variable " + v.Name
			}
		}

		for expr, where := range exprs {
			stripped := stripLabelValues(expr)
			if stripped == "" {
				continue // label_values(label) names no metric to check
			}
			queriesChecked++
			node, err := promqlParser.ParseExpr(stripped)
			if err != nil {
				t.Errorf("%s: %s: unparseable PromQL %q: %v", f, where, expr, err)
				continue
			}
			parser.Inspect(node, func(n parser.Node, _ []parser.Node) error {
				vs, ok := n.(*parser.VectorSelector)
				if !ok {
					return nil
				}
				if !ourNamespace(vs.Name) || vs.Name == "" {
					return nil
				}
				if !names[vs.Name] {
					t.Errorf("%s: %s: references %q, which this exporter never emits", f, where, vs.Name)
				}
				return nil
			})
		}
	}

	// A glob that matched nothing, or a JSON shape that yielded zero panels,
	// would make this test (and the two below) pass vacuously.
	if queriesChecked == 0 {
		t.Fatal("no queries were parsed across any dashboard; the checks above would pass vacuously")
	}
}

func TestEveryLabelMatcherExistsOnItsMetric(t *testing.T) {
	names, labels := emitted(t)

	for _, f := range dashboardFiles(t) {
		d := load(t, f)
		for _, p := range flatten(d.Panels) {
			for _, tgt := range p.Targets {
				if tgt.Expr == "" {
					continue
				}
				node, err := promqlParser.ParseExpr(stripLabelValues(tgt.Expr))
				if err != nil {
					continue // reported by the previous test
				}
				parser.Inspect(node, func(n parser.Node, _ []parser.Node) error {
					vs, ok := n.(*parser.VectorSelector)
					if !ok || !ourNamespace(vs.Name) || !names[vs.Name] {
						return nil
					}
					for _, m := range vs.LabelMatchers {
						if m.Name == "__name__" || m.Name == "job" || m.Name == "instance" {
							continue
						}
						if !labels[vs.Name][m.Name] {
							// A matcher on a nonexistent label renders a blank
							// panel, never an error.
							t.Errorf("%s: panel %q: %s has no label %q", f, p.Title, vs.Name, m.Name)
						}
					}
					return nil
				})
			}
		}
	}
}

func TestNoUnboundDatasourceInputs(t *testing.T) {
	for _, f := range dashboardFiles(t) {
		d := load(t, f)

		// Grafana EXPORT files carry __inputs placeholders that only the import
		// wizard binds. Provisioning never runs the wizard, so every panel
		// pinned to one renders "datasource not found".
		if len(d.Inputs) > 0 {
			t.Errorf("%s: has an __inputs block; strip it", f)
		}
		if len(d.Requires) > 0 {
			t.Errorf("%s: has a __requires block; strip it", f)
		}

		declared := map[string]bool{}
		for _, v := range d.Templating.List {
			if v.Type == "datasource" {
				declared[v.Name] = true
			}
			// A null datasource silently queries Grafana's DEFAULT datasource
			// rather than the selected one.
			if string(v.Datasource) == "null" {
				t.Errorf("%s: template variable %q has a null datasource", f, v.Name)
			}
		}

		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, ref := range datasourceRefs(string(b)) {
			if !declared[ref] {
				t.Errorf("%s: ${%s} does not resolve to a declared datasource variable", f, ref)
			}
		}
	}
}

// labelValuesRe matches Grafana's label_values() wrapper, which is a Grafana
// function and not PromQL — the parser rejects it outright. Both arities are
// used in the wild: label_values(label) and label_values(metric, label).
var labelValuesRe = regexp.MustCompile(`(?s)^\s*label_values\s*\((.*),\s*[A-Za-z_][A-Za-z0-9_]*\s*\)\s*$`)

// stripLabelValues unwraps a label_values() call to the PromQL inside it.
// A single-argument call has no inner expression and yields "".
func stripLabelValues(expr string) string {
	m := labelValuesRe.FindStringSubmatch(expr)
	if m == nil {
		return expr
	}
	return strings.TrimSpace(m[1])
}

// templateQuery extracts a template variable's query. Grafana stores it as a
// bare string in older schema versions and as an object in newer ones; a
// dashboard in this repo may be either.
func templateQuery(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Query
	}
	return ""
}

// datasourceRefVar matches ${NAME} interpolations.
var datasourceRefVar = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

// datasourceRefs returns every distinct datasource-shaped ${...} reference in
// the raw dashboard text. Only DS_-prefixed names and the conventional
// "datasource" name are datasource references; ${pool} and friends are
// ordinary template variables and are not checked here.
func datasourceRefs(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range datasourceRefVar.FindAllStringSubmatch(s, -1) {
		name := m[1]
		if !strings.HasPrefix(name, "DS_") && name != "datasource" {
			continue
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}
