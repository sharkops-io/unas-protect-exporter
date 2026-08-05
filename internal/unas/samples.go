package unas

import (
	"context"
	"time"

	"gitlab.com/sharkops/exporters/exporter-common/metric"
)

// Samples maps a storage payload onto the metric set. Pure, with now
// injected, so the mapping is testable without a clock or a device.
//
// The `pool` label is the pool ID: the device reports no pool name, and disk
// payloads reference pools by that same ID. Protection levels live on raid
// groups rather than pools, hence the extra `raid_group` label on those two.
// Each label map is shared by the samples that carry it and is never mutated
// after construction.
//
// deviceName is the fallback for the device-level `device` label when the
// device reports no name of its own. Disk and pool series need no such label —
// serials and pool UUIDs are already unique across devices — but the
// device-level series would otherwise be identical for two UNAS devices scraped
// from this same pod, and two devices scraped by one exporter would otherwise
// collide.
func Samples(st *Storage, deviceName string, now time.Time) []metric.Sample {
	out := make([]metric.Sample, 0, 96)

	for _, d := range st.Disks {
		l := map[string]string{"slot": d.SlotID, "serial": d.Serial}
		out = append(out,
			metric.Gauge("unas_disk_health_score", "UNAS disk health score, 0-5 as reported by the device.", d.HealthScore,
				map[string]string{"slot": d.SlotID, "serial": d.Serial, "model": d.Model, "type": d.Type}),
			metric.Gauge("unas_disk_temperature_celsius", "UNAS disk temperature in degrees Celsius.", d.Temperature, l),
			metric.Gauge("unas_disk_power_on_hours", "UNAS disk SMART power-on hours.", d.PowerOnHours, l),
			metric.Counter("unas_disk_bad_sector_count_total", "UNAS disk SMART bad sector count.", d.BadSectorCount, l),
			metric.Counter("unas_disk_uncorrectable_sector_count_total", "UNAS disk SMART uncorrectable sector count.", d.UncorrectableSectorCount, l),
			metric.Counter("unas_disk_smart_read_error_count_total", "UNAS disk SMART read error count.", d.SmartReadErrorCount, l),
			metric.Gauge("unas_disk_read_error_rate", "UNAS disk SMART raw read-error-rate attribute.", d.ReadErrorRate, l),
			metric.Gauge("unas_disk_read_kbps", "UNAS disk read throughput in kilobytes per second.", d.ReadKBPS, l),
			metric.Gauge("unas_disk_write_kbps", "UNAS disk write throughput in kilobytes per second.", d.WriteKBPS, l),
			metric.Gauge("unas_disk_state", "UNAS disk state; always 1, read the state label.", 1,
				map[string]string{"slot": d.SlotID, "serial": d.Serial, "state": d.State}),
		)
	}

	for _, p := range st.Pools {
		pl := map[string]string{"pool": p.ID}
		out = append(out,
			metric.Gauge("unas_pool_capacity_bytes", "UNAS storage pool capacity in bytes.", p.Capacity, pl),
			metric.Gauge("unas_pool_used_bytes", "UNAS storage pool bytes in use.", p.Usage, pl),
			metric.Gauge("unas_pool_scrub_progress_ratio", "UNAS storage pool data-scrub progress, 0-1.", p.DataScrubbing.Progress/100, pl),
			metric.Gauge("unas_pool_status", "UNAS storage pool status; always 1, read the status label.", 1,
				map[string]string{"pool": p.ID, "status": p.Status}),
		)
		for _, rg := range p.RaidGroups {
			rl := map[string]string{"pool": p.ID, "raid_group": rg.ID}
			out = append(out,
				metric.Gauge("unas_pool_current_protection", "Disk failures the RAID group can currently absorb.", rg.CurrentProtection, rl),
				metric.Gauge("unas_pool_expected_protection", "Disk failures the RAID group is configured to absorb.", rg.ExpectedProtection, rl),
			)
		}
	}

	// The whole device block is skipped when device-info did not arrive. Its
	// fetch is best-effort (see Client.GetStorage), so a single transient 500
	// would otherwise publish a 0 °C CPU, 0-byte total memory and an empty
	// unas_info — permanently, since Prometheus never corrects a recorded
	// sample. This is the same reasoning that already omits unas_uptime_seconds
	// without a startup time; a gap is honest, a fabricated zero is not.
	if st.HasDeviceInfo {
		di := st.DeviceInfo
		device := di.Name
		if device == "" {
			device = deviceName
		}
		dl := map[string]string{"device": device}
		out = append(out,
			metric.Gauge("unas_cpu_load_ratio", "UNAS device CPU load, 0-1.", di.CPU.CurrentLoad, dl),
			metric.Gauge("unas_cpu_temperature_celsius", "UNAS device CPU temperature in degrees Celsius.", di.CPU.Temperature, dl),
			// The device reports memory in KiB.
			metric.Gauge("unas_memory_total_bytes", "UNAS device total memory in bytes.", di.Memory.Total*1024, dl),
			metric.Gauge("unas_memory_available_bytes", "UNAS device available memory in bytes.", di.Memory.Available*1024, dl),
			metric.Gauge("unas_info", "UNAS device build info; always 1, read the labels.", 1,
				map[string]string{"device": device, "model": di.Model, "firmware": di.FirmwareVersion, "version": di.Version}),
		)
		// device-info can arrive without a parseable startupTime, so this stays
		// a separate guard: it would otherwise read as a 56-year uptime.
		if !di.StartupTime.IsZero() {
			out = append(out, metric.Gauge("unas_uptime_seconds", "Seconds since the UNAS device booted.",
				now.Sub(di.StartupTime.Time).Seconds(), dl))
		}
	}

	return out
}

// Source adapts a Client to collector.Source. The client is built once and
// reused across scrapes so its HTTP connection pool survives.
type Source struct {
	Client     *Client
	DeviceName string
}

func (s *Source) Namespace() string { return "unas" }

func (s *Source) Collect(ctx context.Context) ([]metric.Sample, error) {
	st, err := s.Client.GetStorage(ctx)
	if err != nil {
		return nil, err
	}
	return Samples(st, s.DeviceName, time.Now()), nil
}
