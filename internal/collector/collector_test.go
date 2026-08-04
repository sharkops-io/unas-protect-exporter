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
	panicOn bool          // when true, Collect panics instead of returning
	block   chan struct{} // when non-nil, Collect waits on it
}

func (f *fakeSource) Namespace() string { return f.ns }

func (f *fakeSource) Collect(ctx context.Context) ([]metric.Sample, error) {
	if f.panicOn {
		panic("device driver exploded")
	}
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

// TestCollectDropsDuplicateSeriesAndStillGathers pins the guard that keeps a
// single device payload from blanking the endpoint.
//
// Two UniFi Protect cameras may carry the same name (Protect does not enforce
// uniqueness, and two freshly adopted cameras both have none), and
// protect_camera_recording_enabled is keyed on the name alone. Without the
// dedupe in Collect, the two identical series make the registry's Gather fail
// outright — every metric in this namespace lost, not just the duplicate.
//
// The assertion is deliberately made through a REGISTRY's Gather rather than
// on the Collector directly: collecting from a Collector never notices a
// duplicate, so a direct-Collect assertion would pass with the guard removed.
func TestCollectDropsDuplicateSeriesAndStillGathers(t *testing.T) {
	dup := map[string]string{"camera": "Front Door"}
	src := &fakeSource{ns: "protect", samples: []metric.Sample{
		metric.Gauge("protect_camera_recording_enabled", "Whether recording is enabled for the camera.", 1, dup),
		metric.Gauge("protect_camera_recording_enabled", "Whether recording is enabled for the camera.", 0,
			map[string]string{"camera": "Front Door"}),
		metric.Gauge("protect_camera_up", "Whether the camera is connected to Protect.", 1, dup),
	}}

	registry := prometheus.NewRegistry()
	registry.MustRegister(New(src, time.Second))

	got, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather failed on a payload containing a duplicate series: %v", err)
	}

	families := map[string]int{}
	for _, mf := range got {
		families[mf.GetName()] = len(mf.GetMetric())
	}
	if n := families["protect_camera_recording_enabled"]; n != 1 {
		t.Errorf("protect_camera_recording_enabled series = %d, want 1 (first duplicate wins)", n)
	}
	// The unrelated series in the same payload, and the self-monitoring gauges
	// the collector-health alerts read, must survive the drop.
	if n := families["protect_camera_up"]; n != 1 {
		t.Errorf("protect_camera_up series = %d, want 1", n)
	}
	if n := families["protect_scrape_collector_success"]; n != 1 {
		t.Errorf("protect_scrape_collector_success series = %d, want 1", n)
	}
}

// TestPanickingCollectorDoesNotBlankTheOther covers the other way one
// collector can fail a whole Gather. client_golang recovers a Collect panic
// into an InvalidMetric plus a gather error rather than crashing the process —
// which means a panicking Protect collector still produces a non-nil error
// from Gather, and under the handler's old HTTPErrorOnError that error was a
// 500 with an empty body. The registry itself still yields the healthy
// collector's series alongside the error, and this pins that: the /metrics
// handler's ContinueOnError (cmd/unas-protect-exporter) is what turns these
// series into a served response.
func TestPanickingCollectorDoesNotBlankTheOther(t *testing.T) {
	good := New(&fakeSource{ns: "unas", samples: []metric.Sample{
		metric.Gauge("unas_thing", "thing", 7, nil),
	}}, time.Second)
	boom := New(&fakeSource{ns: "protect", panicOn: true}, time.Second)

	registry := prometheus.NewRegistry()
	registry.MustRegister(good, boom)

	got, err := registry.Gather()
	if err == nil {
		t.Fatal("expected the panic to surface as a gather error")
	}
	names := map[string]bool{}
	for _, mf := range got {
		names[mf.GetName()] = true
	}
	if !names["unas_thing"] {
		t.Error("unas_thing missing: a panic in the protect collector took the unas series with it")
	}
	if !names["unas_scrape_collector_success"] {
		t.Error("unas_scrape_collector_success missing after a panic in the other collector")
	}
}
