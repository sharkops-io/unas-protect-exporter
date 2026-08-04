// Package testutil provides test helpers shared across this repo's packages.
// It lives under internal/ so it stays out of the public API.
package testutil

import "github.com/prometheus/client_golang/prometheus"

// MetricSlice renders a fixed slice of already-built metrics, so exposition
// output can be asserted without any collector machinery.
type MetricSlice []prometheus.Metric

func (c MetricSlice) Describe(chan<- *prometheus.Desc) {}

func (c MetricSlice) Collect(ch chan<- prometheus.Metric) {
	for _, m := range c {
		ch <- m
	}
}
