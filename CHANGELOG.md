# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows the metric-surface compatibility contract described
in `CONTRIBUTING.md` — a renamed or removed metric name is a breaking change.

## [Unreleased]

### Fixed

- `examples/rules.yaml` was missing a `UNASDiskReadErrors` alert on
  `unas_disk_smart_read_error_count_total` (increase() over 24h, same shape
  as the uncorrectable/bad sector alerts) — the metric was always emitted by
  the `unas` collector, it just had no example alert. Added, with a
  `promtool test` pair in `examples/rules_test.yaml`.

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
