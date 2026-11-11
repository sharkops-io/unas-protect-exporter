// Command unas-protect-exporter serves Prometheus metrics for a UniFi UNAS and
// a UniFi Protect console.
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sharkops-io/exporter-common/collector"
	"github.com/sharkops-io/unas-protect-exporter/internal/config"
	"github.com/sharkops-io/unas-protect-exporter/internal/protect"
	"github.com/sharkops-io/unas-protect-exporter/internal/unas"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cfg, err := config.Parse(os.Args[1:], os.Getenv)
	if errors.Is(err, config.ErrHelpRequested) {
		// kingpin already wrote the usage text; --help is success, not a
		// configuration error, so this exits 0 like any conventional CLI.
		os.Exit(0)
	}
	if err != nil {
		// --log.level/--log.format live in the very config that failed to
		// parse, so this one message necessarily goes out on the default handler.
		slog.Error("configuration error", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(cfg.LogHandler(os.Stderr)))

	slog.Info("starting unas-protect-exporter", "version", version)

	reg, err := buildRegistry(cfg)
	if err != nil {
		slog.Error("startup", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle(cfg.TelemetryPath, metricsHandler(reg))
	// Deliberately NO /healthz. A probe that collects would couple this pod's
	// restarts to the NAS and NVR being reachable; a broken exporter should
	// surface as a DOWN Prometheus target instead.
	slog.Info("listening", "address", cfg.ListenAddress, "path", cfg.TelemetryPath)
	if err := http.ListenAndServe(cfg.ListenAddress, mux); err != nil {
		slog.Error("http server", "err", err)
		os.Exit(1)
	}
}

// buildRegistry wires the configured collectors onto a fresh registry. It is
// split out of main, rather than inlined there, so the whole config → registry
// → /metrics path is reachable from a test: that path is the only place all of
// this exporter's seams meet, and it is where the ErrorHandling bug below went
// unnoticed through five review rounds of per-package tests.
func buildRegistry(cfg *config.Config) (*prometheus.Registry, error) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	if cfg.UNASEnabled {
		client, err := unas.New(cfg.UNAS)
		if err != nil {
			return nil, fmt.Errorf("unas client: %w", err)
		}
		reg.MustRegister(collector.New(&unas.Source{Client: client, DeviceName: cfg.UNASName}, cfg.Timeout))
		slog.Info("unas collector enabled", "url", cfg.UNAS.URL)
	}

	// A second collector against the SAME device, with a different credential.
	// See internal/unas/remotebackup.go for why the API key cannot serve this
	// endpoint, and RemoteBackupSource.Namespace for why it does not share the
	// "unas" namespace.
	if cfg.RemoteBackupEnabled {
		client, err := unas.NewRemoteBackup(cfg.UNAS)
		if err != nil {
			return nil, fmt.Errorf("unas remote-backup client: %w", err)
		}
		reg.MustRegister(collector.New(&unas.RemoteBackupSource{Client: client}, cfg.Timeout))
		slog.Info("unas remote-backup collector enabled", "url", cfg.UNAS.URL)
	}

	if cfg.ProtectEnabled {
		client, err := protect.New(cfg.Protect)
		if err != nil {
			return nil, fmt.Errorf("protect client: %w", err)
		}
		reg.MustRegister(collector.New(&protect.Source{Client: client, DeviceName: cfg.ProtectName}, cfg.Timeout))
		slog.Info("protect collector enabled", "url", cfg.Protect.URL)
	}

	if !cfg.UNASEnabled && !cfg.ProtectEnabled {
		return nil, errors.New("no collectors enabled; set --unas.url and/or --protect.url")
	}

	return reg, nil
}

// metricsHandler serves reg at the telemetry path.
//
// ErrorHandling is ContinueOnError, and that is load-bearing rather than
// stylistic. The zero value is HTTPErrorOnError, under which ANY error from a
// single Gather — a duplicate series produced by one collector, or a panic
// inside one collector, both of which the registry turns into a gather error
// rather than a crash — is answered with HTTP 500 and an empty body. That
// throws away every metric on the endpoint: the healthy device's entire
// namespace, the Go and process series, and both _scrape_collector_success
// gauges, so the very alerts meant to catch a broken collector go absent at
// exactly the moment they are needed.
//
// ContinueOnError serves everything that gathered cleanly with HTTP 200 and
// records the failure on promhttp_metric_handler_errors_total{cause="gathering"},
// which is the honest shape: partial data plus a counter saying so. It is what
// makes the isolation this exporter's design promises actually hold. Registry
// is also set so that counter exists on this endpoint at all.
//
// InstrumentMetricHandler wraps the handler to also register
// promhttp_metric_handler_requests_total (by HTTP status code) and
// promhttp_metric_handler_requests_in_flight on reg — the scrape-request
// counters that HandlerFor alone does not produce. It only wraps; it does not
// change ErrorHandling, which stays ContinueOnError.
func metricsHandler(reg *prometheus.Registry) http.Handler {
	h := promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		Registry:      reg,
		ErrorHandling: promhttp.ContinueOnError,
	})
	return promhttp.InstrumentMetricHandler(reg, h)
}
