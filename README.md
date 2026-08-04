# unas-protect-exporter

A Prometheus exporter for two UniFi devices: a **UniFi UNAS** storage
appliance and a **UniFi Protect** console (NVR + cameras + sensors +
bridges). It speaks each device's own local UniFi OS API directly — no
Home Assistant, no ubnt-edge, no third-party API layer.

## This is not a UniFi *Network* controller exporter

If you are looking for metrics about UniFi Network — access points, switches,
routers, WLANs, client counts — this is the wrong project. At least six
existing projects are named `unifi-exporter` (or similarly) and cover exactly
that surface. This exporter deliberately does not: it covers the storage
appliance (UNAS) and the video/sensor console (Protect) only, neither of
which those projects touch. Confusing the two wastes everyone's time — check
which API a given "UniFi exporter" actually talks to before adopting it.

## Quick start

```bash
docker run --rm -p 9101:9101 \
  -e UNAS_URL="https://<unas-host>" \
  -e UNAS_API_KEY="<unas-local-api-key>" \
  -e PROTECT_URL="https://<protect-console-host>" \
  -e PROTECT_USERNAME="<local-account-username>" \
  -e PROTECT_PASSWORD="<local-account-password>" \
  unas-protect-exporter:latest
```

Then scrape `http://localhost:9101/metrics`. Either collector can be omitted:
leaving `UNAS_URL` (or `PROTECT_URL`) unset disables that collector entirely,
so a single instance of this exporter can run against just a UNAS, just a
Protect console, or both.

Flags always take precedence over the environment variables above, which
exist so a Helm values file can stay fully declarative.

## Flag reference

| Flag | Environment | Default | Description |
|---|---|---|---|
| `--web.listen-address` | — | `:9101` | Address to listen on. |
| `--web.telemetry-path` | — | `/metrics` | Path under which to expose metrics. |
| `--collector.timeout` | — | `25s` | Per-collector timeout for a whole scrape. Keep below Prometheus' `scrape_timeout`. |
| `--http.timeout` | — | `10s` | Per-HTTP-request timeout. **Raise this too when raising `--collector.timeout`** — see below. |
| `--log.level` | — | `info` | `debug`, `info`, `warn` or `error`. |
| `--log.format` | — | `logfmt` | `logfmt` or `json`. |
| `--collector.unas` | — | `true` | Enable the UNAS collector. |
| `--collector.protect` | — | `true` | Enable the Protect collector. |
| `--unas.url` | `UNAS_URL` | — | UniFi OS URL of the UNAS. |
| `--unas.api-key` | `UNAS_API_KEY` | — | UNAS local API key. |
| `--unas.name` | `UNAS_NAME` | — | Fallback `device` label when the device reports no name. |
| `--unas.insecure-skip-verify` | — | `false` | Disable TLS verification for the UNAS. |
| `--protect.url` | `PROTECT_URL` | — | UniFi OS URL of the Protect console. |
| `--protect.username` | `PROTECT_USERNAME` | — | UniFi OS local account username. |
| `--protect.password` | `PROTECT_PASSWORD` | — | UniFi OS local account password. |
| `--protect.name` | `PROTECT_NAME` | — | Fallback `device` label when the console reports no name. |
| `--protect.insecure-skip-verify` | — | `false` | Disable TLS verification for the Protect console. |

A collector actually runs only when it is not explicitly disabled **and** has
a URL configured — so both collectors can be left entirely unconfigured
without passing `--collector.unas=false` / `--collector.protect=false`
explicitly. If a URL is set, the matching credential (`unas.api-key`, or both
`protect.username` and `protect.password`) is required; the exporter refuses
to start otherwise rather than starting up in a state that can never
successfully scrape.

Run `unas-protect-exporter --help` for the same list generated straight from
the binary.

### The two timeouts are not the same timeout

`--collector.timeout` bounds one collector's whole scrape. `--http.timeout`
bounds each individual HTTP request inside it, and a scrape is more than one
request: the UNAS collector fetches `/storage` **and** `/systems/device-info`,
and the Protect collector may log in before fetching the bootstrap (and
re-login and retry once on a 401).

So raising `--collector.timeout` alone does nothing for a slow device — every
request is still cut off at `--http.timeout`. Raise both. Conversely,
`--collector.timeout` should comfortably exceed `--http.timeout` × the number
of requests a scrape makes, or the collector deadline will fire mid-sequence
and the extra per-request headroom is unreachable.

A device that is slow enough to need this is usually a device with a real
problem; prefer investigating it over raising both numbers past Prometheus'
`scrape_timeout`.

### Logging volume during an outage

Every failed collection logs one `level=ERROR msg="collection failed"` line.
A device that stays unreachable for hours therefore produces one such line per
scrape interval, indefinitely — there is no rate limiting and no
first-occurrence-only suppression. The `*_scrape_collector_success` gauge is
the signal you should alert on; if the log volume is a problem for your log
pipeline, `--log.level=error` is the floor and beyond that the answer is a
drop rule on your collector, not a flag here.

## Metric reference

Generated from the mappers' own fixtures — `internal/unas/fixture.go` and
`internal/protect/fixture.go` — via `go run ./internal/tools/metricdoc`,
specifically so this table cannot drift from the code that emits these
series. Two `protect_camera_wifi_*` rows and the four
`*_scrape_collector_*` rows are annotated in that tool's source because no
fixture branch produces them (see the comments there for why); everything
else below is literally the generator's output, unedited.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `protect_bridge_info` | gauge | `bridge`, `firmware_version`, `mac`, `type` | Bridge build info; always 1, read the labels. |
| `protect_bridge_last_seen_timestamp_seconds` | gauge | `bridge` | Unix timestamp the bridge was last seen. |
| `protect_bridge_state` | gauge | `bridge`, `state` | Bridge state; always 1, read the state label. |
| `protect_bridge_up` | gauge | `bridge`, `mac`, `type` | Whether the Protect bridge is connected. |
| `protect_camera_info` | gauge | `camera`, `firmware_version`, `mac`, `type` | Camera build info; always 1, read the labels. |
| `protect_camera_last_motion_timestamp_seconds` | gauge | `camera` | Unix timestamp of the camera's last motion event. |
| `protect_camera_last_seen_timestamp_seconds` | gauge | `camera` | Unix timestamp the camera was last seen. |
| `protect_camera_locked_recordings_size_bytes` | gauge | `camera`, `channel`, `kind` | Bytes of locked recordings stored for this camera channel. |
| `protect_camera_recording_enabled` | gauge | `camera` | Whether recording is enabled for the camera. |
| `protect_camera_recordings_size_bytes` | gauge | `camera`, `channel`, `kind` | Bytes of recordings stored for this camera channel. |
| `protect_camera_state` | gauge | `camera`, `state` | Camera state; always 1, read the state label. |
| `protect_camera_up` | gauge | `camera`, `mac`, `type` | Whether the camera is connected to Protect. |
| `protect_camera_wifi_signal_quality` | gauge | `camera` | Camera wifi signal quality, 0-100. |
| `protect_camera_wifi_signal_strength_dbm` | gauge | `camera` | Camera wifi signal strength in dBm. |
| `protect_nvr_cpu_load_ratio` | gauge | `device` | UniFi Protect NVR CPU load, 0-1. |
| `protect_nvr_cpu_temperature_celsius` | gauge | `device` | UniFi Protect NVR CPU temperature in degrees Celsius. |
| `protect_nvr_disk_healthy` | gauge | `device`, `serial`, `slot` | Whether the NVR reports this disk as healthy. |
| `protect_nvr_disk_size_bytes` | gauge | `device`, `model`, `serial`, `slot` | UniFi Protect NVR disk capacity in bytes. |
| `protect_nvr_disk_state` | gauge | `device`, `serial`, `slot`, `state` | UniFi Protect NVR disk state; always 1, read the state label. |
| `protect_nvr_info` | gauge | `device`, `firmware_version`, `market_name`, `release_channel`, `version` | UniFi Protect NVR build info; always 1, read the labels. |
| `protect_nvr_last_seen_timestamp_seconds` | gauge | `device` | Unix timestamp the NVR was last seen. |
| `protect_nvr_memory_available_bytes` | gauge | `device` | UniFi Protect NVR available memory in bytes. |
| `protect_nvr_memory_total_bytes` | gauge | `device` | UniFi Protect NVR total memory in bytes. |
| `protect_nvr_storage_available_bytes` | gauge | `device` | UniFi Protect NVR recording storage bytes free. |
| `protect_nvr_storage_ok` | gauge | `device` | Whether the NVR reports its recording storage as ok. |
| `protect_nvr_storage_recycling` | gauge | `device` | Whether the NVR is recycling old recordings. |
| `protect_nvr_storage_total_bytes` | gauge | `device` | UniFi Protect NVR recording storage capacity in bytes. |
| `protect_nvr_storage_used_bytes` | gauge | `device` | UniFi Protect NVR recording storage bytes in use. |
| `protect_scrape_collector_duration_seconds` | gauge |  | Duration of the last collection for this collector, in seconds. |
| `protect_scrape_collector_success` | gauge |  | Whether the last collection for this collector succeeded (1) or failed (0). |
| `protect_sensor_alarm_triggered_timestamp_seconds` | gauge | `sensor` | Unix timestamp of the sensor's last such event; absent means never. |
| `protect_sensor_battery_low` | gauge | `sensor` | Whether the sensor reports a low battery. |
| `protect_sensor_battery_percent` | gauge | `sensor` | Sensor battery charge, 0-100. |
| `protect_sensor_bt_signal_quality` | gauge | `sensor` | Sensor Bluetooth signal quality, 0-100. |
| `protect_sensor_bt_signal_strength_dbm` | gauge | `sensor` | Sensor Bluetooth signal strength in dBm. |
| `protect_sensor_humidity_ratio` | gauge | `sensor` | Sensor relative humidity, 0-1. |
| `protect_sensor_info` | gauge | `firmware_version`, `mac`, `mount_type`, `sensor`, `type` | Sensor build info; always 1, read the labels. |
| `protect_sensor_is_open` | gauge | `sensor` | Whether an entry-mounted sensor reads open. |
| `protect_sensor_last_seen_timestamp_seconds` | gauge | `sensor` | Unix timestamp the sensor was last seen (unreliable for BT sensors). |
| `protect_sensor_leak_detected_timestamp_seconds` | gauge | `sensor` | Unix timestamp of the sensor's last such event; absent means never. |
| `protect_sensor_light_level` | gauge | `sensor` | Sensor ambient light level as reported by the device. |
| `protect_sensor_motion_detected` | gauge | `sensor` | Whether the sensor currently detects motion. |
| `protect_sensor_motion_detected_timestamp_seconds` | gauge | `sensor` | Unix timestamp of the sensor's last such event; absent means never. |
| `protect_sensor_state` | gauge | `sensor`, `state` | Sensor state; always 1, read the state label. |
| `protect_sensor_tampering_detected_timestamp_seconds` | gauge | `sensor` | Unix timestamp of the sensor's last such event; absent means never. |
| `protect_sensor_temperature_celsius` | gauge | `sensor` | Sensor temperature in degrees Celsius. |
| `protect_sensor_up` | gauge | `mount_type`, `sensor`, `type` | Whether the sensor is connected to Protect. |
| `unas_cpu_load_ratio` | gauge | `device` | UNAS device CPU load, 0-1. |
| `unas_cpu_temperature_celsius` | gauge | `device` | UNAS device CPU temperature in degrees Celsius. |
| `unas_disk_bad_sector_count_total` | counter | `serial`, `slot` | UNAS disk SMART bad sector count. |
| `unas_disk_health_score` | gauge | `model`, `serial`, `slot`, `type` | UNAS disk health score, 0-5 as reported by the device. |
| `unas_disk_power_on_hours` | gauge | `serial`, `slot` | UNAS disk SMART power-on hours. |
| `unas_disk_read_error_rate` | gauge | `serial`, `slot` | UNAS disk SMART raw read-error-rate attribute. |
| `unas_disk_read_kbps` | gauge | `serial`, `slot` | UNAS disk read throughput in kilobytes per second. |
| `unas_disk_smart_read_error_count_total` | counter | `serial`, `slot` | UNAS disk SMART read error count. |
| `unas_disk_state` | gauge | `serial`, `slot`, `state` | UNAS disk state; always 1, read the state label. |
| `unas_disk_temperature_celsius` | gauge | `serial`, `slot` | UNAS disk temperature in degrees Celsius. |
| `unas_disk_uncorrectable_sector_count_total` | counter | `serial`, `slot` | UNAS disk SMART uncorrectable sector count. |
| `unas_disk_write_kbps` | gauge | `serial`, `slot` | UNAS disk write throughput in kilobytes per second. |
| `unas_info` | gauge | `device`, `firmware`, `model`, `version` | UNAS device build info; always 1, read the labels. |
| `unas_memory_available_bytes` | gauge | `device` | UNAS device available memory in bytes. |
| `unas_memory_total_bytes` | gauge | `device` | UNAS device total memory in bytes. |
| `unas_pool_capacity_bytes` | gauge | `pool` | UNAS storage pool capacity in bytes. |
| `unas_pool_current_protection` | gauge | `pool`, `raid_group` | Disk failures the RAID group can currently absorb. |
| `unas_pool_expected_protection` | gauge | `pool`, `raid_group` | Disk failures the RAID group is configured to absorb. |
| `unas_pool_scrub_progress_ratio` | gauge | `pool` | UNAS storage pool data-scrub progress, 0-1. |
| `unas_pool_status` | gauge | `pool`, `status` | UNAS storage pool status; always 1, read the status label. |
| `unas_pool_used_bytes` | gauge | `pool` | UNAS storage pool bytes in use. |
| `unas_scrape_collector_duration_seconds` | gauge |  | Duration of the last collection for this collector, in seconds. |
| `unas_scrape_collector_success` | gauge |  | Whether the last collection for this collector succeeded (1) or failed (0). |
| `unas_uptime_seconds` | gauge | `device` | Seconds since the UNAS device booted. |

The table above covers the series this exporter *maps from device payloads*.
The endpoint also serves three families it does not generate itself, which are
worth knowing about before you write a recording rule or a cardinality budget
against it:

- `go_*` and `process_*` — the standard Go runtime and process collectors,
  registered by default. Drop them with a `metric_relabel_configs` action if
  you do not want them.
- `promhttp_*` — in particular
  `promhttp_metric_handler_errors_total{cause="gathering"}`. This exporter
  serves partial results rather than failing a whole scrape (see below), so
  that counter is how a gather error becomes visible at all. A non-zero
  `increase()` on it means some series were dropped from a response that
  otherwise looked fine; it is worth an alert.

A field that is null on the source device is **omitted from the sample**,
never emitted as zero — most visibly the `*_timestamp_seconds` "last event"
series (`protect_sensor_leak_detected_timestamp_seconds` and friends): an
absent series means "never happened", which is what lets alerts on them be
recency checks rather than latches that can never clear once tripped.

## Three things that are not obvious

**One bad series does not blank the endpoint.** `/metrics` is served with
`promhttp.ContinueOnError`: if one collector panics, or somehow emits a
duplicate series, the response is still HTTP 200 carrying everything that
gathered cleanly — the other device's entire namespace, the runtime families,
and both `*_scrape_collector_success` gauges, so the collector-health alerts in
`examples/rules.yaml` can still fire. The failure is recorded on
`promhttp_metric_handler_errors_total{cause="gathering"}` instead of being
turned into an empty HTTP 500. Alert on that counter; a scrape that looks
healthy while quietly missing series is the one failure mode this design trades
for.

**TLS verification is on by default.** `--unas.insecure-skip-verify` and
`--protect.insecure-skip-verify` both default to `false`. A UNAS or Protect
console usually serves either a self-signed cert or one from an internal CA;
if that certificate expires, or the exporter doesn't trust the CA that issued
it, collection breaks outright rather than degrading — you'll see the
affected `*_scrape_collector_success` gauge drop to `0` and every `unas_*` or
`protect_*` series go stale/absent. Watch certificate expiry on whatever
issued the device's cert, not just on the exporter itself.

**There is deliberately no `/healthz` endpoint.** A liveness/readiness probe
that actually collects from the NAS or NVR would couple this pod's restart
behavior to whether those devices happen to be reachable at that instant —
turning a transient network blip on the NAS into a Kubernetes-restart storm
on the exporter, which does nothing to fix the underlying problem and adds
noise on top of it. A broken exporter should surface as a `DOWN` Prometheus
scrape target (i.e. via the `up` metric and `*_scrape_collector_success`),
not as a probe result. There is therefore nothing at `/healthz` to wire a
`livenessProbe`/`readinessProbe` to; if you need one, point it at TCP-connect
on the listen port instead.

## Dashboards and alerts

`dashboards/` and `examples/` in **this** repository are the canonical
copies. Both are validated offline in CI against the metrics this exporter
actually emits — `dashboards/dashboards_test.go` parses every panel query and
template-variable query in `dashboards/*.json` and fails the build if one
references a metric or label this exporter doesn't produce, and the `rules`
CI stage runs `promtool check rules` / `promtool test rules` against
`examples/rules.yaml`.

**If you are adopting these from outside this org**, both dashboards hard-code
their Prometheus datasource as `"uid": "prometheus"`. That UID is whatever your
Grafana assigned when the datasource was provisioned, and it is almost
certainly not `prometheus` — Grafana generates a random UID unless a
provisioning file pins one. Every panel will render "Datasource not found"
until you either rename your datasource's UID to `prometheus` or
search-and-replace it in `dashboards/*.json`. (Converting the dashboards to a
`datasource` template variable would be the portable fix; they are not written
that way today.) The `"uid": "-- Grafana --"` in `unas.json` is the built-in
annotations datasource and needs no change.

`examples/rules.yaml` similarly assumes both collectors are running. If you
deploy with only one device configured, delete the `absent()` pair for the
other — the file says so inline.

Downstream Kubernetes deployments (Grafana sidecar ConfigMaps,
`additionalPrometheusRulesMap` entries, etc.) vendor copies of these files.
If you change a panel or an alert here, update it here first — a downstream
copy that drifts from this repo has no such CI check protecting it.

## Building

```bash
docker build -t unas-protect-exporter:local .
```

CI builds and pushes the image with
[Kaniko](https://github.com/GoogleContainerTools/kaniko) via the shared
`sharkops/gitlab-ci-common-config` `docker/build.yml` template — no
Docker-in-Docker. That builder is **single-architecture**: it produces
whatever platform the Kaniko executor itself runs on (`linux/amd64` on this
org's shared runners), not the `linux/amd64,linux/arm64` multi-arch manifest
a `docker buildx` pipeline could produce. This is an accepted trade for this
deployment — the target cluster is amd64-only — but is worth knowing if this
image is ever pulled onto arm64 hardware (e.g. a Raspberry Pi or Apple
Silicon dev machine): it won't run there without a local `docker build`.

## License

MIT. See `LICENSE`.
