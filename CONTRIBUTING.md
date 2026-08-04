# Contributing

## Running the checks locally

```bash
go build ./...
go test ./... -race -count=1
go vet ./...
gofmt -l .            # must print nothing
golangci-lint run --timeout 5m
docker build -t unas-protect-exporter:test .
docker run --rm unas-protect-exporter:test --help
```

`go test` covers, among other things:

- Golden exposition tests for both the `unas` and `protect` mappers — each
  asserts the exact Prometheus text output for a fixture, not just "no error".
- Collector isolation (`internal/collector`) — one collector's failure or
  panic must not affect the other's series.
- Session reuse (`internal/unifios`, `internal/protect`) — a scrape must not
  re-authenticate every interval.
- Dashboard validation (`dashboards/dashboards_test.go`) — every PromQL query
  and label matcher shipped in `dashboards/*.json` is checked against the
  metric and label set the mappers actually emit, entirely offline.
- `promtool check rules` / `promtool test rules` against `examples/rules.yaml`
  (run in CI's `rules` stage, not by `go test`).

## New metrics need golden exposition tests

If you add a metric, add a test that pins its exact Prometheus exposition
text (name, type, labels, help, value) via
`promtestutil.CollectAndCompare`, not just a test that the mapper "doesn't
error". The dashboard and metric-reference-generation tooling
(`internal/tools/metricdoc`) both derive their notion of "what this exporter
emits" from the mappers' fixtures, so an untested metric is invisible to that
tooling too — see `internal/unas/fixture.go` and `internal/protect/fixture.go`
for what the fixtures need to cover, and why.

## Metric names are a compatibility contract

**A metric name, once shipped, does not change.** The same goes for a help
string's meaning, a label key, and a label's set of valid values in the sense
that alerting or dashboard queries depend on it. Downstream deployments build
dashboards, alerts and recording rules against these names; renaming one
silently breaks every one of them at the next scrape, with no error anywhere
in the chain.

If a metric genuinely needs to be renamed or removed:

1. Ship the new name alongside the old one for at least one release, if it is
   possible to compute both from the same source data.
2. Document the rename in `CHANGELOG.md` under a `### Changed` (or `### Removed`)
   heading, explicitly.
3. Bump the **major** version. This project follows the Prometheus exporter
   convention of treating metric-surface changes as breaking changes,
   independent of whether any Go API changed.

Adding a new metric, or a new label to an *existing* metric that doesn't
change any existing series identity, is not breaking and does not require a
major bump.
