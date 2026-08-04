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

// Describe sends nothing, deliberately. client_golang treats a Collector as
// "checked" the moment Describe yields even one Desc, and a checked
// collector's pedantic registry (which is what prometheus/client_golang's
// testutil.CollectAndCount/CollectAndCompare always register against) then
// rejects any Collect'd metric whose descriptor wasn't pre-declared here. The
// device series are built with NewConstMetric from payloads whose label sets
// aren't known until collection, so they can never be pre-declared — meaning
// declaring even the two self-monitoring descriptors here would poison the
// whole collector as "checked" and cause every real scrape to fail gathering.
// Sending nothing keeps this collector fully unchecked, which is the correct
// state for one whose metric set is inherently dynamic.
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
