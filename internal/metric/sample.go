// Package metric holds the one intermediate representation this exporter uses
// between a device payload and Prometheus exposition.
//
// It exists so the mapping logic ported from another-app compiles unchanged. It
// is deliberately smaller than its ancestor: another-app needed a registry-wide
// snapshot map, plus help-text and label-dimension guards, because many
// integrations shared one unchecked collector and could disagree about a name.
// Here each collector owns its own output, so none of that applies.
//
// Duplicate-series detection does still apply, and is the one guard that
// survived the port: a duplicate can arise WITHIN a single device payload
// (two UniFi Protect cameras may carry the same name), and a duplicate fails
// the whole gather regardless of how many collectors are registered. See
// Signature.
package metric

import (
	"fmt"
	"sort"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
)

// Kind is the Prometheus metric type a Sample renders as. The zero value is a
// gauge, which is what nearly every sample is.
type Kind int

const (
	KindGauge Kind = iota
	KindCounter
)

func (k Kind) valueType() prometheus.ValueType {
	if k == KindCounter {
		return prometheus.CounterValue
	}
	return prometheus.GaugeValue
}

// Sample is one metric reading. Labels must not be mutated after construction.
type Sample struct {
	Name   string
	Help   string
	Labels map[string]string
	Value  float64
	Kind   Kind
}

// Gauge builds a gauge Sample.
func Gauge(name, help string, value float64, labels map[string]string) Sample {
	return Sample{Name: name, Help: help, Labels: labels, Value: value}
}

// Counter builds a monotonic-counter Sample — used for SMART lifetime counts,
// so range functions like increase() handle a device reset correctly.
func Counter(name, help string, value float64, labels map[string]string) Sample {
	return Sample{Name: name, Help: help, Labels: labels, Value: value, Kind: KindCounter}
}

// Metric renders the Sample. The error is returned rather than swallowed so a
// caller can log which series was dropped; client_golang would otherwise fail
// the entire gather, taking every other metric with it.
func (s Sample) Metric() (prometheus.Metric, error) {
	// client_golang (as of v1.24.1, via prometheus/common v0.70.1) validates
	// NewDesc's fqName against model.UTF8Validation directly — it does not
	// consult the package-level model.NameValidationScheme escape hatch at
	// all, so a malformed name like "not a valid name" (invalid legacy syntax,
	// but valid UTF-8) would otherwise sail through NewDesc/NewConstMetric
	// without error. This exporter only ever emits classic snake_case names,
	// so check against the strict legacy scheme ourselves before building the
	// Desc.
	if !model.LegacyValidation.IsValidMetricName(s.Name) {
		return nil, fmt.Errorf("metric: %q is not a valid metric name", s.Name)
	}

	keys, values := sortedLabels(s.Labels)
	help := s.Help
	if help == "" {
		help = "Exported from a UniFi device payload."
	}
	return prometheus.NewConstMetric(
		prometheus.NewDesc(s.Name, help, keys, nil), s.Kind.valueType(), s.Value, values...)
}

// Signature identifies the TIME SERIES a Sample renders to: the metric name
// plus its label values in sorted-key order, which is exactly the tuple
// Prometheus uses to tell two series of one metric apart.
//
// Two Samples that share a Signature are the same series, and client_golang
// rejects both of them together — "collected metric ... was collected before
// with the same name and label values" fails the ENTIRE gather, not just the
// offending family. A caller holding a slice of Samples must therefore drop
// later duplicates itself; see collector.Collect.
//
// The label keys are deliberately not part of the signature. Every metric name
// this exporter emits carries a fixed set of keys (they are literals in the
// mappers), so values alone are already a faithful series identity, and this
// matches the guard another-app' metrics bridge used for the same purpose.
func (s Sample) Signature() string {
	_, values := sortedLabels(s.Labels)
	return s.Name + "\x00" + strings.Join(values, "\x00")
}

// sortedLabels returns label keys and their values in matching sorted order,
// which is what NewConstMetric's positional values argument requires.
func sortedLabels(labels map[string]string) (keys, values []string) {
	if len(labels) == 0 {
		return nil, nil
	}
	keys = make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	values = make([]string, 0, len(keys))
	for _, k := range keys {
		values = append(values, labels[k])
	}
	return keys, values
}
