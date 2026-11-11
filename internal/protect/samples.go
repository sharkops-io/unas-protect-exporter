package protect

import (
	"context"
	"strconv"
	"strings"

	"github.com/sharkops-io/exporter-common/metric"
)

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// msToSeconds converts a Protect epoch-milliseconds field to Prometheus'
// float seconds.
func msToSeconds(ms int64) float64 { return float64(ms) / 1000 }

// Samples maps a bootstrap payload onto the metric set. Pure, so the mapping is
// testable without a device. Unlike unas.Samples it takes no clock: Protect has
// no uptime metric (nvr.uptime is null on this platform), so there is no
// staleness logic here that would need one.
//
// Two rules govern every optional field here, and both exist because Prometheus
// never corrects a sample once recorded:
//
//   - A null source is OMITTED, never zeroed. Series absence therefore means
//     "never happened" — which is what lets the leak alert be a recency check
//     rather than a latch that can never clear.
//   - A stale source is omitted too. A disconnected camera keeps reporting its
//     last wifi numbers, and signalStrength 0 reads as a perfect signal.
//
// deviceName is the fallback for the NVR `device` label when the console
// reports no name, mirroring unas.Samples' handling of the same label.
func Samples(bs *Bootstrap, deviceName string) []metric.Sample {
	out := make([]metric.Sample, 0, 64)
	out = append(out, protectNVRSamples(bs, deviceName)...)
	out = append(out, protectCameraSamples(bs)...)
	out = append(out, protectSensorSamples(bs)...)
	out = append(out, protectBridgeSamples(bs)...)
	return out
}

func protectNVRSamples(bs *Bootstrap, integrationName string) []metric.Sample {
	nvr := bs.NVR
	device := strings.TrimSpace(nvr.Name)
	if device == "" {
		device = integrationName
	}
	dl := map[string]string{"device": device}
	si := nvr.SystemInfo

	out := []metric.Sample{}
	if si.CPU.AverageLoad != nil {
		// averageLoad is a percentage (21.2 == 21.2%), so a ratio divides by 100.
		out = append(out, metric.Gauge("protect_nvr_cpu_load_ratio",
			"UniFi Protect NVR CPU load, 0-1.", *si.CPU.AverageLoad/100, dl))
	}
	if si.CPU.Temperature != nil {
		out = append(out, metric.Gauge("protect_nvr_cpu_temperature_celsius",
			"UniFi Protect NVR CPU temperature in degrees Celsius.", *si.CPU.Temperature, dl))
	}
	out = append(out,
		// The console reports memory in KiB.
		metric.Gauge("protect_nvr_memory_total_bytes", "UniFi Protect NVR total memory in bytes.",
			float64(si.Memory.Total)*1024, dl),
		metric.Gauge("protect_nvr_memory_available_bytes", "UniFi Protect NVR available memory in bytes.",
			float64(si.Memory.Available)*1024, dl),
		metric.Gauge("protect_nvr_storage_total_bytes", "UniFi Protect NVR recording storage capacity in bytes.",
			float64(si.Storage.Size), dl),
		metric.Gauge("protect_nvr_storage_used_bytes", "UniFi Protect NVR recording storage bytes in use.",
			float64(si.Storage.Used), dl),
		metric.Gauge("protect_nvr_storage_available_bytes", "UniFi Protect NVR recording storage bytes free.",
			float64(si.Storage.Available), dl),
		metric.Gauge("protect_nvr_storage_ok", "Whether the NVR reports its recording storage as ok.",
			b2f(si.Storage.Capability == "ok"), dl),
		metric.Gauge("protect_nvr_storage_recycling", "Whether the NVR is recycling old recordings.",
			b2f(si.Storage.IsRecycling), dl),
		metric.Gauge("protect_nvr_info", "UniFi Protect NVR build info; always 1, read the labels.", 1,
			map[string]string{"device": device, "version": nvr.Version,
				"firmware_version": nvr.FirmwareVersion, "market_name": nvr.MarketName,
				"release_channel": nvr.ReleaseChannel}),
	)
	if nvr.LastSeen != nil {
		out = append(out, metric.Gauge("protect_nvr_last_seen_timestamp_seconds",
			"Unix timestamp the NVR was last seen.", msToSeconds(*nvr.LastSeen), dl))
	}

	for _, d := range si.UStorage.Disks {
		slot := strconv.Itoa(d.Slot)
		out = append(out,
			metric.Gauge("protect_nvr_disk_size_bytes", "UniFi Protect NVR disk capacity in bytes.",
				float64(d.Size), map[string]string{"device": device, "slot": slot, "model": d.Model, "serial": d.Serial}),
			metric.Gauge("protect_nvr_disk_healthy", "Whether the NVR reports this disk as healthy.",
				b2f(d.Healthy == "good"), map[string]string{"device": device, "slot": slot, "serial": d.Serial}),
			metric.Gauge("protect_nvr_disk_state", "UniFi Protect NVR disk state; always 1, read the state label.", 1,
				map[string]string{"device": device, "slot": slot, "serial": d.Serial, "state": d.State}),
		)
	}
	return out
}

func protectCameraSamples(bs *Bootstrap) []metric.Sample {
	out := []metric.Sample{}
	for _, c := range bs.Cameras {
		name := strings.TrimSpace(c.Name)
		cl := map[string]string{"camera": name}

		out = append(out,
			metric.Gauge("protect_camera_up", "Whether the camera is connected to Protect.",
				b2f(c.IsConnected), map[string]string{"camera": name, "type": strings.TrimSpace(c.Type), "mac": c.MAC}),
			metric.Gauge("protect_camera_state", "Camera state; always 1, read the state label.", 1,
				map[string]string{"camera": name, "state": c.State}),
			// isRecording is CONFIGURED INTENT: it reads true on a camera that
			// has been disconnected for weeks. Named accordingly.
			metric.Gauge("protect_camera_recording_enabled", "Whether recording is enabled for the camera.",
				b2f(c.IsRecording), cl),
			metric.Gauge("protect_camera_info", "Camera build info; always 1, read the labels.", 1,
				map[string]string{"camera": name, "type": strings.TrimSpace(c.Type), "mac": c.MAC,
					"firmware_version": c.FirmwareVersion}),
		)
		if c.LastSeen != nil {
			out = append(out, metric.Gauge("protect_camera_last_seen_timestamp_seconds",
				"Unix timestamp the camera was last seen.", msToSeconds(*c.LastSeen), cl))
		}
		if c.LastMotion != nil {
			out = append(out, metric.Gauge("protect_camera_last_motion_timestamp_seconds",
				"Unix timestamp of the camera's last motion event.", msToSeconds(*c.LastMotion), cl))
		}

		// Wifi readings are only meaningful while connected: a disconnected
		// camera keeps reporting its last numbers, and signalStrength 0 reads
		// as a perfect signal. See the "stale source is omitted" rule on
		// Samples.
		if c.IsConnected {
			if c.Stats.Wifi.SignalQuality != nil {
				out = append(out, metric.Gauge("protect_camera_wifi_signal_quality",
					"Camera wifi signal quality, 0-100.", *c.Stats.Wifi.SignalQuality, cl))
			}
			if c.Stats.Wifi.SignalStrength != nil {
				out = append(out, metric.Gauge("protect_camera_wifi_signal_strength_dbm",
					"Camera wifi signal strength in dBm.", *c.Stats.Wifi.SignalStrength, cl))
			}
		}

		// channelStorage is a per-channel map, not a scalar; stats.storage.used
		// is null on this platform.
		for channel, cs := range c.Stats.Storage.ChannelStorage {
			for kind, rec := range map[string]RecordingSize{
				"rotating": cs.Rotating, "timelapse": cs.Timelapse,
			} {
				l := map[string]string{"camera": name, "channel": channel, "kind": kind}
				out = append(out,
					metric.Gauge("protect_camera_recordings_size_bytes",
						"Bytes of recordings stored for this camera channel.",
						float64(rec.RecordingsSizeBytes), l),
					metric.Gauge("protect_camera_locked_recordings_size_bytes",
						"Bytes of locked recordings stored for this camera channel.",
						float64(rec.LockedRecordingsSizeBytes), l),
				)
			}
		}
	}
	return out
}

func protectSensorSamples(bs *Bootstrap) []metric.Sample {
	out := []metric.Sample{}
	for _, s := range bs.Sensors {
		name := strings.TrimSpace(s.Name)
		sl := map[string]string{"sensor": name}

		out = append(out,
			metric.Gauge("protect_sensor_up", "Whether the sensor is connected to Protect.",
				b2f(s.IsConnected), map[string]string{"sensor": name, "type": strings.TrimSpace(s.Type),
					"mount_type": s.MountType}),
			metric.Gauge("protect_sensor_state", "Sensor state; always 1, read the state label.", 1,
				map[string]string{"sensor": name, "state": s.State}),
			metric.Gauge("protect_sensor_battery_low", "Whether the sensor reports a low battery.",
				b2f(s.BatteryStatus.IsLow), sl),
			metric.Gauge("protect_sensor_motion_detected", "Whether the sensor currently detects motion.",
				b2f(s.IsMotionDetected), sl),
			metric.Gauge("protect_sensor_info", "Sensor build info; always 1, read the labels.", 1,
				map[string]string{"sensor": name, "type": strings.TrimSpace(s.Type), "mac": s.MAC,
					"mount_type": s.MountType, "firmware_version": s.FirmwareVersion}),
		)
		if s.BatteryStatus.Percentage != nil {
			out = append(out, metric.Gauge("protect_sensor_battery_percent",
				"Sensor battery charge, 0-100.", *s.BatteryStatus.Percentage, sl))
		}
		if s.BluetoothConnectionState.SignalQuality != nil {
			out = append(out, metric.Gauge("protect_sensor_bt_signal_quality",
				"Sensor Bluetooth signal quality, 0-100.", *s.BluetoothConnectionState.SignalQuality, sl))
		}
		if s.BluetoothConnectionState.SignalStrength != nil {
			out = append(out, metric.Gauge("protect_sensor_bt_signal_strength_dbm",
				"Sensor Bluetooth signal strength in dBm.", *s.BluetoothConnectionState.SignalStrength, sl))
		}
		// NOTE: lastSeen runs days stale on a healthy BT sensor. It is exported
		// for completeness but must not be used as a liveness signal.
		if s.LastSeen != nil {
			out = append(out, metric.Gauge("protect_sensor_last_seen_timestamp_seconds",
				"Unix timestamp the sensor was last seen (unreliable for BT sensors).",
				msToSeconds(*s.LastSeen), sl))
		}
		if s.IsOpened != nil {
			out = append(out, metric.Gauge("protect_sensor_is_open",
				"Whether an entry-mounted sensor reads open.", b2f(*s.IsOpened), sl))
		}

		// Event timestamps: omitted when null, so absence means never-detected.
		for metricName, event := range map[string]*int64{
			"protect_sensor_leak_detected_timestamp_seconds":      s.LeakDetectedAt,
			"protect_sensor_tampering_detected_timestamp_seconds": s.TamperingDetectedAt,
			"protect_sensor_alarm_triggered_timestamp_seconds":    s.AlarmTriggeredAt,
			"protect_sensor_motion_detected_timestamp_seconds":    s.MotionDetectedAt,
		} {
			if event != nil {
				out = append(out, metric.Gauge(metricName,
					"Unix timestamp of the sensor's last such event; absent means never.",
					msToSeconds(*event), sl))
			}
		}

		// Capability-driven readings. All three conditions are required: the
		// hardware advertises the capability (KEY PRESENCE — the values are
		// objects, not booleans), the reading is non-null, and its status is not
		// "unknown". The deployed leak-mounted sensor advertises temperature,
		// humidity and light while reporting none of them.
		out = append(out, sensorReadingSample(s, "temperature", "protect_sensor_temperature_celsius",
			"Sensor temperature in degrees Celsius.", s.Stats.Temperature, 1, sl)...)
		out = append(out, sensorReadingSample(s, "humidity", "protect_sensor_humidity_ratio",
			"Sensor relative humidity, 0-1.", s.Stats.Humidity, 0.01, sl)...)
		out = append(out, sensorReadingSample(s, "light", "protect_sensor_light_level",
			"Sensor ambient light level as reported by the device.", s.Stats.Light, 1, sl)...)
	}
	return out
}

// sensorReadingSample returns zero or one sample for a mount-dependent reading.
// scale converts the device's unit to the metric's (humidity is a percentage
// published as a ratio).
func sensorReadingSample(s Sensor, flag, name, help string,
	r SensorReading, scale float64, labels map[string]string) []metric.Sample {
	if _, advertised := s.FeatureFlags[flag]; !advertised {
		return nil
	}
	if r.Value == nil || r.Status == "unknown" {
		return nil
	}
	return []metric.Sample{metric.Gauge(name, help, *r.Value*scale, labels)}
}

func protectBridgeSamples(bs *Bootstrap) []metric.Sample {
	out := []metric.Sample{}
	for _, b := range bs.Bridges {
		name := strings.TrimSpace(b.Name)
		bl := map[string]string{"bridge": name}
		out = append(out,
			metric.Gauge("protect_bridge_up", "Whether the Protect bridge is connected.",
				b2f(b.IsConnected), map[string]string{"bridge": name, "type": strings.TrimSpace(b.Type), "mac": b.MAC}),
			metric.Gauge("protect_bridge_state", "Bridge state; always 1, read the state label.", 1,
				map[string]string{"bridge": name, "state": b.State}),
			metric.Gauge("protect_bridge_info", "Bridge build info; always 1, read the labels.", 1,
				map[string]string{"bridge": name, "type": strings.TrimSpace(b.Type), "mac": b.MAC,
					"firmware_version": b.FirmwareVersion}),
		)
		if b.LastSeen != nil {
			out = append(out, metric.Gauge("protect_bridge_last_seen_timestamp_seconds",
				"Unix timestamp the bridge was last seen.", msToSeconds(*b.LastSeen), bl))
		}
	}
	return out
}

// Source adapts a Client to collector.Source. Reusing one Client across scrapes
// is required, not just an optimisation: the client holds a UniFi OS cookie
// session, and rebuilding it per scrape would POST /api/auth/login every
// scrape interval, forever.
type Source struct {
	Client     *Client
	DeviceName string
}

func (s *Source) Namespace() string { return "protect" }

func (s *Source) Collect(ctx context.Context) ([]metric.Sample, error) {
	bs, err := s.Client.GetBootstrap(ctx)
	if err != nil {
		return nil, err
	}
	return Samples(bs, s.DeviceName), nil
}
