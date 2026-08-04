// Command unas-protect-exporter serves Prometheus metrics for a UniFi UNAS and
// a UniFi Protect console.
package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/collector"
	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/config"
	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/protect"
	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/unas"
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
		slog.Error("configuration error", "err", err)
		os.Exit(1)
	}

	slog.Info("starting unas-protect-exporter", "version", version)

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	if cfg.UNASEnabled {
		client, err := unas.New(cfg.UNAS)
		if err != nil {
			slog.Error("unas client", "err", err)
			os.Exit(1)
		}
		reg.MustRegister(collector.New(&unas.Source{Client: client, DeviceName: cfg.UNASName}, cfg.Timeout))
		slog.Info("unas collector enabled", "url", cfg.UNAS.URL)
	}

	if cfg.ProtectEnabled {
		client, err := protect.New(cfg.Protect)
		if err != nil {
			slog.Error("protect client", "err", err)
			os.Exit(1)
		}
		reg.MustRegister(collector.New(&protect.Source{Client: client, DeviceName: cfg.ProtectName}, cfg.Timeout))
		slog.Info("protect collector enabled", "url", cfg.Protect.URL)
	}

	if !cfg.UNASEnabled && !cfg.ProtectEnabled {
		slog.Error("no collectors enabled; set --unas.url and/or --protect.url")
		os.Exit(1)
	}

	http.Handle(cfg.TelemetryPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	// Deliberately NO /healthz. A probe that collects would couple this pod's
	// restarts to the NAS and NVR being reachable; a broken exporter should
	// surface as a DOWN Prometheus target instead.
	slog.Info("listening", "address", cfg.ListenAddress, "path", cfg.TelemetryPath)
	if err := http.ListenAndServe(cfg.ListenAddress, nil); err != nil {
		slog.Error("http server", "err", err)
		os.Exit(1)
	}
}
