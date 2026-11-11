package unas

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sharkops-io/exporter-common/testutil"
)

func collectorFor(t *testing.T, st *Storage, name string, now time.Time) prometheus.Collector {
	t.Helper()
	var out testutil.MetricSlice
	for _, s := range Samples(st, name, now) {
		m, err := s.Metric()
		if err != nil {
			t.Fatalf("sample %q did not render: %v", s.Name, err)
		}
		out = append(out, m)
	}
	return out
}

func testStorage() *Storage { return FixtureStorage() }

func TestSamplesDiskExposition(t *testing.T) {
	c := collectorFor(t, testStorage(), "fallback", time.Now())

	expected := `
# HELP unas_disk_temperature_celsius UNAS disk temperature in degrees Celsius.
# TYPE unas_disk_temperature_celsius gauge
unas_disk_temperature_celsius{serial="REDACTED1",slot="1"} 45
# HELP unas_disk_bad_sector_count_total UNAS disk SMART bad sector count.
# TYPE unas_disk_bad_sector_count_total counter
unas_disk_bad_sector_count_total{serial="REDACTED1",slot="1"} 0
# HELP unas_disk_state UNAS disk state; always 1, read the state label.
# TYPE unas_disk_state gauge
unas_disk_state{serial="REDACTED1",slot="1",state="optimal"} 1
`
	if err := promtestutil.CollectAndCompare(c, strings.NewReader(expected),
		"unas_disk_temperature_celsius", "unas_disk_bad_sector_count_total", "unas_disk_state"); err != nil {
		t.Fatal(err)
	}
}

func TestSamplesPoolExposition(t *testing.T) {
	c := collectorFor(t, testStorage(), "fallback", time.Now())

	// Scrub progress is a ratio: the device reports a percentage and the mapper
	// divides by 100.
	expected := `
# HELP unas_pool_scrub_progress_ratio UNAS storage pool data-scrub progress, 0-1.
# TYPE unas_pool_scrub_progress_ratio gauge
unas_pool_scrub_progress_ratio{pool="pool-uuid-1"} 0.38
# HELP unas_pool_status UNAS storage pool status; always 1, read the status label.
# TYPE unas_pool_status gauge
unas_pool_status{pool="pool-uuid-1",status="scrubbing"} 1
# HELP unas_pool_current_protection Disk failures the RAID group can currently absorb.
# TYPE unas_pool_current_protection gauge
unas_pool_current_protection{pool="pool-uuid-1",raid_group="raid-group-1"} 2
`
	if err := promtestutil.CollectAndCompare(c, strings.NewReader(expected),
		"unas_pool_scrub_progress_ratio", "unas_pool_status", "unas_pool_current_protection"); err != nil {
		t.Fatal(err)
	}
}

func TestSamplesMemoryIsConvertedFromKiB(t *testing.T) {
	c := collectorFor(t, testStorage(), "fallback", time.Now())

	// The device reports KiB; the mapper multiplies by 1024. 8272256 KiB = 8470790144 B.
	expected := `
# HELP unas_memory_total_bytes UNAS device total memory in bytes.
# TYPE unas_memory_total_bytes gauge
unas_memory_total_bytes{device="ExampleNAS"} 8.470790144e+09
`
	if err := promtestutil.CollectAndCompare(c, strings.NewReader(expected), "unas_memory_total_bytes"); err != nil {
		t.Fatal(err)
	}
}

func TestSamplesOmitsDeviceBlockWithoutDeviceInfo(t *testing.T) {
	st := testStorage()
	st.HasDeviceInfo = false

	samples := Samples(st, "fallback", time.Now())
	if len(samples) == 0 {
		t.Fatal("Samples returned nothing; the absence checks below would pass vacuously")
	}

	// A best-effort device-info failure must publish NOTHING device-level rather
	// than a 0 degC CPU and an empty unas_info, which Prometheus would never correct.
	var sawDiskSeries bool
	for _, s := range samples {
		switch s.Name {
		case "unas_cpu_load_ratio", "unas_cpu_temperature_celsius", "unas_memory_total_bytes",
			"unas_memory_available_bytes", "unas_info", "unas_uptime_seconds":
			t.Errorf("device-level series %q published without device info", s.Name)
		case "unas_disk_temperature_celsius":
			sawDiskSeries = true
		}
	}
	// Disks must survive a missing device-info fetch: only the device block is
	// best-effort, so an empty result here would mean the guard over-corrected.
	if !sawDiskSeries {
		t.Error("unas_disk_temperature_celsius was not emitted; disks should survive missing device info")
	}
}

func TestSamplesUsesDeviceNameFallback(t *testing.T) {
	st := testStorage()
	st.DeviceInfo.Name = ""

	var found bool
	for _, s := range Samples(st, "fallback-name", time.Now()) {
		if s.Name == "unas_cpu_load_ratio" {
			found = true
			if s.Labels["device"] != "fallback-name" {
				t.Errorf("device label = %q, want the fallback", s.Labels["device"])
			}
		}
	}
	// Without this, the loop above matches nothing and the test passes
	// vacuously if unas_cpu_load_ratio is ever dropped entirely.
	if !found {
		t.Fatal("unas_cpu_load_ratio was not emitted at all")
	}
}

func TestSamplesOmitsUptimeWithoutStartupTime(t *testing.T) {
	st := testStorage()
	st.DeviceInfo.StartupTime = StartupTime{} // FixtureStorage sets a real one; zero it here

	samples := Samples(st, "fallback", time.Now())
	if len(samples) == 0 {
		t.Fatal("Samples returned nothing; the absence check below would pass vacuously")
	}

	// An unparseable startupTime would otherwise read as a 56-year uptime.
	for _, s := range samples {
		if s.Name == "unas_uptime_seconds" {
			t.Fatal("uptime published without a startup time")
		}
	}
}

// parseLinkSpeed must never guess. A wrong number silently misreports the
// link; a missing series is visible.
func TestParseLinkSpeed(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float64
		ok   bool
	}{
		{"10 GbE", 10e9, true},
		// A bare unit means 1 of it. This is the case that invites a parser to
		// return 0 and call it a day.
		{"GbE", 1e9, true},
		{"2.5 GbE", 2.5e9, true},
		{"100 MbE", 100e6, true},
		// No link. Must NOT become 0 bit/s, which would be indistinguishable
		// from a real reading on a port that is up.
		{"", 0, false},
		{"   ", 0, false},
		// Unrecognised on purpose rather than guessed at.
		{"10 Gbps", 0, false},
		{"fast", 0, false},
		{"GbE 10", 0, false},
	} {
		got, ok := parseLinkSpeed(tc.in)
		if ok != tc.ok {
			t.Errorf("parseLinkSpeed(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("parseLinkSpeed(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// The disconnected interface in the fixture has LinkSpeed "" and must publish
// no link-speed series at all, while still publishing its up=0 series.
func TestSamplesOmitsLinkSpeedForDownInterface(t *testing.T) {
	var sawDownUp, sawDownSpeed bool
	for _, s := range Samples(FixtureStorage(), "fallback", time.Now()) {
		if s.Labels["name"] != "enp0s1" {
			continue
		}
		switch s.Name {
		case "unas_network_interface_up":
			sawDownUp = true
			if s.Value != 0 {
				t.Errorf("down interface up = %v, want 0", s.Value)
			}
		case "unas_network_interface_link_speed_bits":
			sawDownSpeed = true
		}
	}
	if !sawDownUp {
		t.Error("no unas_network_interface_up for the down interface")
	}
	if sawDownSpeed {
		t.Error("link speed emitted for an interface with no link")
	}
}

// network-io is fetched independently of device-info, so its samples must not
// disappear when device-info is missing.
func TestSamplesEmitsNetworkIOWithoutDeviceInfo(t *testing.T) {
	st := FixtureStorage()
	st.HasDeviceInfo = false
	var got int
	for _, s := range Samples(st, "fallback", time.Now()) {
		if s.Name == "unas_network_receive_kbps" || s.Name == "unas_network_transmit_kbps" {
			got++
			if s.Labels["device"] != "fallback" {
				t.Errorf("device label = %q, want the fallback when device-info is absent", s.Labels["device"])
			}
		}
	}
	if got != 2 {
		t.Errorf("got %d network-io samples without device-info, want 2", got)
	}
}
