// Command metricdoc prints the exporter's metric reference as a markdown
// table, derived from the mappers themselves so it cannot drift from reality.
//
//	go run ./internal/tools/metricdoc > /tmp/metrics.md
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gitlab.com/sharkops/exporters/exporter-common/metric"
	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/protect"
	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/unas"
)

type row struct {
	name, kind, help string
	labels           map[string]bool
}

// fatal satisfies the fixture helper's error interface outside a test.
type fatal struct{}

func (fatal) Fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	rows := map[string]*row{}

	add := func(samples []metric.Sample) {
		for _, s := range samples {
			r, ok := rows[s.Name]
			if !ok {
				kind := "gauge"
				if s.Kind == metric.KindCounter {
					kind = "counter"
				}
				r = &row{name: s.Name, kind: kind, help: s.Help, labels: map[string]bool{}}
				rows[s.Name] = r
			}
			for k := range s.Labels {
				r.labels[k] = true
			}
		}
	}

	add(unas.Samples(unas.FixtureStorage(), "example", time.Now()))
	add(protect.Samples(protect.FixtureBootstrap(fatal{}), "example"))
	add(unas.RemoteBackupSamples(unas.FixtureRemoteBackupTasks(), time.Now()))
	add([]metric.Sample{
		metric.Gauge("unas_scrape_collector_success", "Whether the last collection for this collector succeeded (1) or failed (0).", 1, nil),
		metric.Gauge("unas_scrape_collector_duration_seconds", "Duration of the last collection for this collector, in seconds.", 0, nil),
		metric.Gauge("protect_scrape_collector_success", "Whether the last collection for this collector succeeded (1) or failed (0).", 1, nil),
		metric.Gauge("protect_scrape_collector_duration_seconds", "Duration of the last collection for this collector, in seconds.", 0, nil),
		// The remote-backup collector runs under its own namespace rather than
		// "unas" — see unas.RemoteBackupSource.Namespace — so it produces a
		// third pair of these, which no mapper fixture emits either.
		metric.Gauge("unas_remote_backup_scrape_collector_success", "Whether the last collection for this collector succeeded (1) or failed (0).", 1, nil),
		metric.Gauge("unas_remote_backup_scrape_collector_duration_seconds", "Duration of the last collection for this collector, in seconds.", 0, nil),
		// protect_camera_wifi_signal_{quality,strength_dbm}: real, shipped
		// metrics (samples.go) that only emit while a camera IsConnected. The
		// fixture's sole camera is pinned disconnected on purpose (see
		// TestSamplesCameraExposition / TestSamplesOmitsWifiForDisconnectedCamera
		// in protect/samples_test.go, which golden-pin that state), so it can
		// never exercise this branch. Listed manually for the same reason the
		// scrape_collector series above are: no mapper fixture emits them.
		metric.Gauge("protect_camera_wifi_signal_quality", "Camera wifi signal quality, 0-100.", 0, map[string]string{"camera": ""}),
		metric.Gauge("protect_camera_wifi_signal_strength_dbm", "Camera wifi signal strength in dBm.", 0, map[string]string{"camera": ""}),
	})

	names := make([]string, 0, len(rows))
	for n := range rows {
		names = append(names, n)
	}
	sort.Strings(names)

	fmt.Println("| Metric | Type | Labels | Description |")
	fmt.Println("|---|---|---|---|")
	for _, n := range names {
		r := rows[n]
		labels := make([]string, 0, len(r.labels))
		for l := range r.labels {
			labels = append(labels, "`"+l+"`")
		}
		sort.Strings(labels)
		fmt.Printf("| `%s` | %s | %s | %s |\n", r.name, r.kind, strings.Join(labels, ", "), r.help)
	}
}
