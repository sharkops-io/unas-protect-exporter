// Package collector adapts a device source to prometheus.Collector.
//
// Collection happens at scrape time rather than on a background ticker. This
// exporter has one consumer, so the scrape interval IS the poll rate, which is
// the same position qbittorrent-exporter takes in this cluster. The cost of a
// slow device is a slow scrape of THIS exporter only.
package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/metric"
)

// Source is one device's collection logic. Namespace is the metric prefix the
// source's samples use ("unas", "protect"), and also prefixes this collector's
// own self-monitoring series.
type Source interface {
	Namespace() string
	Collect(ctx context.Context) ([]metric.Sample, error)
}

// Collector wraps a Source with a timeout and self-monitoring series. Each
// Collector is independent: it holds no shared state, so one device failing
// cannot affect another's series.
type Collector struct {
	src     Source
	timeout time.Duration

	successDesc  *prometheus.Desc
	durationDesc *prometheus.Desc
}

func New(src Source, timeout time.Duration) *Collector {
	ns := src.Namespace()
	return &Collector{
		src:     src,
		timeout: timeout,
		successDesc: prometheus.NewDesc(ns+"_scrape_collector_success",
			"Whether the last collection for this collector succeeded (1) or failed (0).", nil, nil),
		durationDesc: prometheus.NewDesc(ns+"_scrape_collector_duration_seconds",
			"Duration of the last collection for this collector, in seconds.", nil, nil),
	}
}

// Describe sends nothing, deliberately.
//
// The Collector interface's documented contract (client_golang
// prometheus/collector.go) requires that the descriptor of every metric sent
// from Collect be one that Describe returned — unless Describe sends no
// descriptor at all, which marks the whole Collector "unchecked" and lifts
// that requirement. The device series here are built with NewConstMetric
// from payloads whose label sets aren't known until collection, so they can
// never be enumerated by Describe up front; declaring the two
// self-monitoring descriptors and nothing else would violate the contract
// for every device series. A plain prometheus.NewRegistry() is lenient about
// this and would gather successfully anyway, but every testutil.CollectAnd*
// helper — and any real NewPedanticRegistry() — enforces it strictly, and
// would reject those series with "unregistered descriptor" at gather time.
// (Verified by running the two-descriptor version against this package's own
// tests: two of five failed with exactly that error.)
//
// The cost of going fully unchecked: an unchecked Collector skips the
// duplicate-descriptor check Register normally performs, so registering two
// Collectors that happen to share a namespace would no longer panic at
// Register time — it would instead surface later, at Gather time, as
// "collected metric ... was collected before with the same name and label
// values" (the label-less success/duration series would collide first).
// Acceptable here because main registers each namespace ("unas", "protect")
// exactly once; a future maintainer wiring up a second instance of either
// (e.g. a second Protect NVR) should know this moved from a startup-time
// guarantee to a scrape-time one.
func (c *Collector) Describe(chan<- *prometheus.Desc) {}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	start := time.Now()
	samples, err := c.src.Collect(ctx)
	elapsed := time.Since(start).Seconds()

	success := 1.0
	if err != nil {
		// Retract rather than serve a stale reading: "no data" is honest,
		// "45 degC, all optimal" recorded during an outage is not. No device
		// samples are emitted on this path at all.
		success = 0
		samples = nil
		slog.Error("collection failed", "collector", c.src.Namespace(), "err", err)
	}

	for _, s := range samples {
		m, merr := s.Metric()
		if merr != nil {
			// One malformed series must not fail the whole gather, which would
			// take every other metric on the endpoint with it.
			slog.Warn("dropping sample", "metric", s.Name, "err", merr)
			continue
		}
		ch <- m
	}

	ch <- prometheus.MustNewConstMetric(c.successDesc, prometheus.GaugeValue, success)
	ch <- prometheus.MustNewConstMetric(c.durationDesc, prometheus.GaugeValue, elapsed)
}
