# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows the metric-surface compatibility contract described
in `CONTRIBUTING.md` — a renamed or removed metric name is a breaking change.

## [Unreleased]

## [0.2.0] - 2026-08-13

### Added

- **UNAS remote-backup collector.** Scrapes
  `/proxy/drive/api/v1/remote-backup/tasks` and reports the state of the
  device's scheduled syncs to an external destination such as Google Drive:
  `unas_remote_backup_task_info`, `_enabled`, `_needs_reauth`,
  `_last_run_status`, `_last_run_error_count`, `_last_run_timestamp_seconds`,
  `_last_run_duration_seconds` and `_next_run_timestamp_seconds`.

  `needs_reauth` is the one to alert on. An expired OAuth grant is how an
  offsite backup dies silently: the task stays configured, the schedule stays
  enabled, and nothing ships.

  **This collector needs a username and password, not the API key.** The
  static `X-API-KEY` returns `403 invalid auth user ID` on that endpoint —
  remote-backup tasks are per-user records — so it authenticates with a UniFi
  OS cookie session via the existing `internal/unifios`, against the same host
  the API-key client uses. One device, two credentials, neither able to reach
  the other's endpoints.

  Set `--unas.username`/`--unas.password` (or `UNAS_USERNAME`/`UNAS_PASSWORD`)
  to enable it. **They are optional**: with the API key alone every existing
  metric is unchanged and only this collector is skipped, so upgrading without
  them is a no-op. Setting exactly one of the pair is rejected as a typo.

  No success/failure boolean is derived from the run status. Only `succeeded`
  has been observed from the device, so the status is carried as a label and
  `_last_run_error_count` — a length, needing no knowledge of the device's
  failure vocabulary — is the numeric signal.

  This collector uses its own `unas_remote_backup` namespace rather than
  sharing `unas`, because two collectors under one namespace emit
  label-identical `<ns>_scrape_collector_success` gauges that the registry
  accepts and Prometheus then reports as a duplicate-series gather error.

- **UNAS network throughput and interface metrics**, from
  `/proxy/drive/api/v2/systems/network-io` and the previously unmapped fields
  of `device-info`: `unas_network_receive_kbps`, `unas_network_transmit_kbps`,
  `unas_network_interface_up`, `unas_network_interface_link_speed_bits`,
  `unas_network_interface_max_speed_bits`, `unas_device_status`,
  `unas_sfp_aggregation_enabled` and `unas_memory_free_bytes`.

  The link-speed pair exists to catch a 10 GbE port quietly renegotiating to
  1 GbE, which halves throughput with nothing else reporting it. The device
  reports speeds as human strings (`"10 GbE"`, `"GbE"` meaning 1 GbE, `""`
  for no link), so they are parsed; anything unrecognised is **omitted rather
  than published as 0**, since a 0 there is indistinguishable from a real
  reading on a port that is up.

  `network-io` is a third best-effort request inside `GetStorage`. Raise
  `--collector.timeout` to more than three times `--http.timeout`, not twice.

  Note these are device-computed **rates**, not monotonic counters: they
  cannot be `rate()`d and a scrape gap loses that interval. That is what the
  endpoint offers, and is already true of `unas_disk_read_kbps`.

- Example alerting rules for the remote-backup collector in
  `examples/rules.yaml`, with `promtool` unit tests asserting both sides of
  every `for:`. All four are task-agnostic: the `task` label names the
  offender, so adding a backup task needs no rule change.

  `UNASRemoteBackupCollectorFailing` is the one that keeps the other three
  honest. Without it a rotated password or a locked account makes the
  collector fail and every remote-backup alert go silent, which is
  indistinguishable from healthy backups.

  Note for anyone writing more of these: an `input_series` interval above
  Prometheus' 5m lookback delta leaves the series stale at eval time, so the
  expression matches nothing and the TEST fails while the ALERT is correct.
  The 27h staleness test uses a 4m interval for exactly that reason.

### Changed

- `httpx`, `metric`, `collector` and `testutil` now come from the shared
  [`exporter-common`](https://github.com/sharkops-io/exporter-common)
  module (v0.1.0) rather than living under `internal/`. Those four packages
  were originally extracted *from* this repo to build `gameserver-exporter`;
  this closes the loop so there is one copy rather than two drifting ones.

  **No metric-surface change.** Verified by running the pre- and
  post-migration binaries side by side against the live UNAS and Protect
  console: all 200 `# HELP`/`# TYPE` lines identical in order, and all 175
  series identities identical — including the four
  `*_scrape_collector_*` self-monitoring series, the only ones whose
  construction actually changed. `go run ./internal/tools/metricdoc` output is
  byte-identical too.

  `internal/config` deliberately stays here: roughly half of it is
  UniFi-shaped and has no business in a device-agnostic module.

## [0.1.2] - 2026-08-04

### Fixed

- The six new "Exporter Health" panels added in 0.1.1
  (`promhttp_metric_handler_errors_total`, `promhttp_metric_handler_requests_total`,
  `go_goroutines`, `go_memstats_alloc_bytes`, `process_resident_memory_bytes`,
  `process_cpu_seconds_total`) queried their metric with no aggregation, so
  each panel leaked the ServiceMonitor's own scrape labels (`pod`,
  `container`, `endpoint`, `instance`) into the legend instead of rendering
  the single series these panels are meant to show. `monitoring`'s
  `verify-dashboards.sh`, which runs range queries against a live
  Prometheus rather than trusting the JSON alone, caught it before it
  shipped. Wrapped each in `max(...)` or `sum(...)`/`sum by (code) (...)`
  as appropriate; no metric, label or panel title changed.

## [0.1.1] - 2026-08-04

### Added

- `/metrics` is now wrapped in `promhttp.InstrumentMetricHandler`, which
  registers `promhttp_metric_handler_requests_total` (by HTTP status `code`)
  and `promhttp_metric_handler_requests_in_flight` on the same registry.
  `ErrorHandling` stays `promhttp.ContinueOnError` — this only wraps the
  existing handler, it does not change how gather errors are handled.
- A collapsed "Exporter Health" row in both `dashboards/unas.json` and
  `dashboards/protect.json`, matching the shape already used elsewhere for
  exporter self-monitoring: collector success/duration, gather errors
  (`promhttp_metric_handler_errors_total{cause="gathering"}` — previously
  alerted on via `ExporterGatheringErrors` but with nowhere to see the
  trend), scrape requests, goroutines, Go heap, RSS and CPU. All new queries
  match on `job="unas-protect-exporter"`, never a `pod=` string.

### Fixed

- `examples/rules.yaml` was missing a `UNASDiskReadErrors` alert on
  `unas_disk_smart_read_error_count_total` (increase() over 24h, same shape
  as the uncorrectable/bad sector alerts) — the metric was always emitted by
  the `unas` collector, it just had no example alert. Added, with a
  `promtool test` pair in `examples/rules_test.yaml`.
- `examples/rules.yaml` shipped an `absent()` backstop pair for the `unas`
  collector (`UNASDiskSeriesAbsent`, `UNASPoolSeriesAbsent`) but nothing
  equivalent for `protect`, even though the same failure mode applies: a
  reshaped bootstrap payload decodes cleanly to an empty slice, so
  `protect_scrape_collector_success` stays 1 while `protect_sensor_up` (and
  the leak/tamper/battery alerting built on it) silently vanishes. Added
  `ProtectSensorSeriesAbsent`, anchored on `protect_sensor_up` rather than
  `protect_camera_up` (which stays present at 0 for a merely disconnected
  camera, making it a weaker canary), with a `promtool test` trio in
  `examples/rules_test.yaml`.

## [0.1.0] - 2026-08-04

Initial release.

### Added

- A `unas` collector exposing UniFi UNAS storage-appliance metrics: device
  info and health (CPU, memory, uptime), per-disk SMART data, and per-pool /
  per-RAID-group capacity and protection state.
- A `protect` collector exposing UniFi Protect console metrics: NVR info,
  CPU/memory/storage/disk health, and per-device state for cameras, sensors
  and bridges.
- Both collectors run independently and can be enabled separately
  (`--collector.unas`, `--collector.protect`); either can be left
  unconfigured.
- Per-collector self-monitoring series (`unas_scrape_collector_success`,
  `unas_scrape_collector_duration_seconds` and the `protect_` equivalents) so
  a device-unreachable failure is visible even though the affected `unas_*` /
  `protect_*` series simply go absent rather than reading zero.
- Session reuse for both UniFi OS integrations: one login per process
  lifetime, not one per scrape.
- Endpoint-level fault isolation: `/metrics` is served with
  `promhttp.ContinueOnError` and each collector drops duplicate series before
  they reach the registry, so a panic or a duplicate in one collector cannot
  answer the whole endpoint with an empty HTTP 500. Gather failures surface on
  `promhttp_metric_handler_errors_total{cause="gathering"}` instead.
- `--http.timeout` (default `10s`), bounding each individual HTTP request.
  `--collector.timeout` bounds a whole scrape and cannot on its own give a
  slow device more time.
- `--log.level` and `--log.format`, wired to `slog`.
- `absent()` alert rules in `examples/rules.yaml` for the two failure modes
  that produce wrong data with `success == 1`: a collector that never
  registered, and a device payload that changed shape and now decodes to
  nothing.
- Example Grafana dashboards (`dashboards/unas.json`, `dashboards/protect.json`)
  and example Prometheus alerting rules with `promtool` unit tests
  (`examples/rules.yaml`, `examples/rules_test.yaml`), both validated offline
  against the metrics the mappers actually emit.
- A generated metric reference (`internal/tools/metricdoc`) so the README's
  metric table cannot drift from the code that produces it.
