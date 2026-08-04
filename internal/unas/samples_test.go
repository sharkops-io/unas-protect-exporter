package unas

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"

	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/testutil"
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

func testStorage() *Storage {
	st := &Storage{
		Disks: []Disk{{
			SlotID: "1", Serial: "REDACTED1", Model: "EXAMPLE-16TB", Type: "HDD",
			State: "optimal", Size: 16000900661248, HealthScore: 5, Temperature: 45,
			PowerOnHours: 11874, BadSectorCount: 0, UncorrectableSectorCount: 0,
			SmartReadErrorCount: 0, ReadErrorRate: 36427417, ReadKBPS: 245760, WriteKBPS: 0,
		}},
		Pools: []Pool{{
			ID: "pool-uuid-1", Number: 1, PreferLevel: "raid6", Status: "scrubbing",
			Capacity: 79960856330240, Usage: 26826967351296,
			DataScrubbing: DataScrubbing{Status: "inProgress", Progress: 38},
			RaidGroups: []RaidGroup{{
				ID: "raid-group-1", Number: 1, ConfigLevel: "raid6", CurrentLevel: "raid6",
				CurrentProtection: 2, ExpectedProtection: 2, Progress: 38,
			}},
		}},
		HasDeviceInfo: true,
	}
	st.DeviceInfo.Name = "ExampleNAS"
	st.DeviceInfo.Model = "UNASPRO"
	st.DeviceInfo.Version = "4.4.7"
	st.DeviceInfo.FirmwareVersion = "5.1.27"
	st.DeviceInfo.CPU.CurrentLoad = 0.069
	st.DeviceInfo.CPU.Temperature = 66
	st.DeviceInfo.Memory.Total = 8272256
	st.DeviceInfo.Memory.Available = 2915712
	return st
}

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

	// A best-effort device-info failure must publish NOTHING device-level rather
	// than a 0 degC CPU and an empty unas_info, which Prometheus would never correct.
	for _, s := range Samples(st, "fallback", time.Now()) {
		switch s.Name {
		case "unas_cpu_load_ratio", "unas_cpu_temperature_celsius", "unas_memory_total_bytes",
			"unas_memory_available_bytes", "unas_info", "unas_uptime_seconds":
			t.Errorf("device-level series %q published without device info", s.Name)
		}
	}
}

func TestSamplesUsesDeviceNameFallback(t *testing.T) {
	st := testStorage()
	st.DeviceInfo.Name = ""

	for _, s := range Samples(st, "fallback-name", time.Now()) {
		if s.Name == "unas_cpu_load_ratio" && s.Labels["device"] != "fallback-name" {
			t.Errorf("device label = %q, want the fallback", s.Labels["device"])
		}
	}
}

func TestSamplesOmitsUptimeWithoutStartupTime(t *testing.T) {
	st := testStorage() // StartupTime left zero

	// An unparseable startupTime would otherwise read as a 56-year uptime.
	for _, s := range Samples(st, "fallback", time.Now()) {
		if s.Name == "unas_uptime_seconds" {
			t.Fatal("uptime published without a startup time")
		}
	}
}
