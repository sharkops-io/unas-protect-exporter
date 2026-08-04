package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/metric"
)

type fakeSource struct {
	ns      string
	samples []metric.Sample
	err     error
	calls   int
	block   chan struct{} // when non-nil, Collect waits on it
}

func (f *fakeSource) Namespace() string { return f.ns }

func (f *fakeSource) Collect(ctx context.Context) ([]metric.Sample, error) {
	f.calls++
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		// Return samples alongside the error, not nil — so a test that
		// asserts retraction is exercising the Collector's own "samples =
		// nil on error" defense, not merely observing that this fixture
		// never handed back anything to retract in the first place.
		return f.samples, f.err
	}
	return f.samples, nil
}

func TestCollectSuccessPublishesSamplesAndSuccessSeries(t *testing.T) {
	src := &fakeSource{ns: "unas", samples: []metric.Sample{
		metric.Gauge("unas_thing", "thing", 7, map[string]string{"id": "a"}),
	}}
	c := New(src, time.Second)

	expected := `
# HELP unas_thing thing
# TYPE unas_thing gauge
unas_thing{id="a"} 7
# HELP unas_scrape_collector_success Whether the last collection for this collector succeeded (1) or failed (0).
# TYPE unas_scrape_collector_success gauge
unas_scrape_collector_success 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected),
		"unas_thing", "unas_scrape_collector_success"); err != nil {
		t.Fatal(err)
	}
}

func TestCollectFailurePublishesNoDeviceSamples(t *testing.T) {
	src := &fakeSource{ns: "unas", err: errors.New("device unreachable"), samples: []metric.Sample{
		metric.Gauge("unas_thing", "thing", 7, map[string]string{"id": "a"}),
	}}
	c := New(src, time.Second)

	// Serving a stale "45 degC, all optimal" during an outage is worse than a gap.
	if n := testutil.CollectAndCount(c, "unas_thing"); n != 0 {
		t.Errorf("device series published despite a failed collection: %d", n)
	}

	expected := `
# HELP unas_scrape_collector_success Whether the last collection for this collector succeeded (1) or failed (0).
# TYPE unas_scrape_collector_success gauge
unas_scrape_collector_success 0
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "unas_scrape_collector_success"); err != nil {
		t.Fatal(err)
	}
}

func TestCollectAlwaysPublishesDuration(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  *fakeSource
	}{
		{"success", &fakeSource{ns: "protect"}},
		{"failure", &fakeSource{ns: "protect", err: errors.New("boom")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(tc.src, time.Second)
			if n := testutil.CollectAndCount(c, "protect_scrape_collector_duration_seconds"); n != 1 {
				t.Errorf("duration series count = %d, want 1", n)
			}
		})
	}
}

func TestCollectorsAreIndependent(t *testing.T) {
	good := New(&fakeSource{ns: "unas", samples: []metric.Sample{
		metric.Gauge("unas_thing", "thing", 7, nil),
	}}, time.Second)
	bad := New(&fakeSource{ns: "protect", err: errors.New("login failed")}, time.Second)

	registry := prometheus.NewRegistry()
	registry.MustRegister(good, bad)

	// A Protect login failure must not blank unas_*.
	if n := testutil.CollectAndCount(registry, "unas_thing"); n != 1 {
		t.Errorf("unas_thing count = %d, want 1 despite the protect collector failing", n)
	}
	if n := testutil.CollectAndCount(registry, "protect_scrape_collector_success"); n != 1 {
		t.Errorf("protect success series missing: %d", n)
	}
}

func TestCollectRespectsTimeout(t *testing.T) {
	src := &fakeSource{ns: "unas", block: make(chan struct{})}
	c := New(src, 20*time.Millisecond)

	// A hung device must fail the collector, not hang the scrape forever.
	done := make(chan struct{})
	go func() {
		testutil.CollectAndCount(c)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("collection did not return after the timeout elapsed")
	}
	close(src.block)
}

func TestCollectDropsMalformedSampleButKeepsOthers(t *testing.T) {
	src := &fakeSource{ns: "unas", samples: []metric.Sample{
		metric.Gauge("not a valid name", "malformed", 1, nil),
		metric.Gauge("unas_valid_thing", "valid", 3, nil),
	}}
	c := New(src, time.Second)

	// One malformed series must not fail the whole gather, which would take
	// every other metric on the endpoint with it — including the valid
	// sample collected alongside it and the self-monitoring series.
	if n := testutil.CollectAndCount(c, "unas_valid_thing"); n != 1 {
		t.Errorf("valid sample count = %d, want 1 despite a malformed sample in the same collection", n)
	}
	if n := testutil.CollectAndCount(c, "unas_scrape_collector_success"); n != 1 {
		t.Errorf("success series missing after a malformed sample was dropped: %d", n)
	}
}
