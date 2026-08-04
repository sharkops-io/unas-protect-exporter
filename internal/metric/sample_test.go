package metric

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// sliceCollector renders a fixed slice of Samples, so the exposition format a
// Sample produces can be asserted without any collector machinery.
type sliceCollector []Sample

func (c sliceCollector) Describe(chan<- *prometheus.Desc) {}

func (c sliceCollector) Collect(ch chan<- prometheus.Metric) {
	for _, s := range c {
		m, err := s.Metric()
		if err != nil {
			continue
		}
		ch <- m
	}
}

func TestGaugeExposition(t *testing.T) {
	c := sliceCollector{
		Gauge("unas_disk_temperature_celsius", "UNAS disk temperature in degrees Celsius.", 45,
			map[string]string{"slot": "1", "serial": "REDACTED1"}),
	}

	// Labels are emitted in sorted key order regardless of map iteration order.
	expected := `
# HELP unas_disk_temperature_celsius UNAS disk temperature in degrees Celsius.
# TYPE unas_disk_temperature_celsius gauge
unas_disk_temperature_celsius{serial="REDACTED1",slot="1"} 45
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Fatal(err)
	}
}

func TestCounterExposition(t *testing.T) {
	c := sliceCollector{
		Counter("unas_disk_bad_sector_count_total", "UNAS disk SMART bad sector count.", 0,
			map[string]string{"slot": "1", "serial": "REDACTED1"}),
	}

	expected := `
# HELP unas_disk_bad_sector_count_total UNAS disk SMART bad sector count.
# TYPE unas_disk_bad_sector_count_total counter
unas_disk_bad_sector_count_total{serial="REDACTED1",slot="1"} 0
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Fatal(err)
	}
}

func TestNoLabels(t *testing.T) {
	c := sliceCollector{Gauge("unas_scrape_collector_success", "Whether the last collection succeeded.", 1, nil)}

	expected := `
# HELP unas_scrape_collector_success Whether the last collection succeeded.
# TYPE unas_scrape_collector_success gauge
unas_scrape_collector_success 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidMetricNameReturnsError(t *testing.T) {
	// A malformed name must surface as an error the caller can log and skip,
	// not as a panic and not as a silently-dropped series.
	if _, err := (Gauge("not a valid name", "help", 1, nil)).Metric(); err == nil {
		t.Fatal("expected an error for an invalid metric name, got nil")
	}
}
