// Package config binds this exporter's flags, each with an environment
// fallback so a Helm values file can stay declarative.
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/alecthomas/kingpin/v2"

	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/protect"
	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/unas"
)

// ErrHelpRequested is returned by Parse when the user passed --help. It is
// not a configuration error — kingpin has already written the usage text to
// its usage writer — so a caller must check for it with errors.Is and exit 0,
// rather than treating it like every other Parse error (which means "print
// this and exit 1").
//
// This exists because Parse calls app.Terminate(nil): kingpin's own
// os.Exit(0)-on-help behavior is disabled on purpose, so that Parse stays
// callable from a test without ending the test binary's process. With
// terminate turned into a no-op, kingpin's Parse falls through --help
// exactly like an empty command line and returns a zero-value Config with a
// nil error — indistinguishable, to a caller, from "the user ran this with
// no flags at all", which cmd/unas-protect-exporter's "no collectors
// enabled" check correctly treats as a fatal misconfiguration. That check is
// load-bearing (an exporter with nothing configured must fail loudly rather
// than serve an empty /metrics forever), so it cannot simply be relaxed;
// --help needs its own, distinct signal instead.
var ErrHelpRequested = errors.New("help requested")

type Config struct {
	ListenAddress string
	TelemetryPath string
	Timeout       time.Duration

	UNASEnabled bool
	UNAS        unas.Config
	UNASName    string

	ProtectEnabled bool
	Protect        protect.Config
	ProtectName    string
}

// Parse builds a Config from args and the environment. getenv is injected so
// the precedence rules are testable without touching the real environment.
func Parse(args []string, getenv func(string) string) (*Config, error) {
	app := kingpin.New("unas-protect-exporter", "Prometheus exporter for UniFi UNAS and UniFi Protect.")
	app.Terminate(nil)

	// Track --help explicitly (see ErrHelpRequested) rather than relying on
	// kingpin's own exit behavior, which is disabled above.
	var helpRequested bool
	app.HelpFlag.IsSetByUser(&helpRequested)

	var cfg Config

	app.Flag("web.listen-address", "Address to listen on.").Default(":9101").StringVar(&cfg.ListenAddress)
	app.Flag("web.telemetry-path", "Path under which to expose metrics.").Default("/metrics").StringVar(&cfg.TelemetryPath)
	app.Flag("collector.timeout", "Per-collector timeout. Keep below Prometheus' scrape_timeout.").
		Default("25s").DurationVar(&cfg.Timeout)

	unasOn := app.Flag("collector.unas", "Enable the UNAS collector.").Default("true").Bool()
	protectOn := app.Flag("collector.protect", "Enable the Protect collector.").Default("true").Bool()

	app.Flag("unas.url", "UniFi OS URL of the UNAS.").StringVar(&cfg.UNAS.URL)
	app.Flag("unas.api-key", "UNAS local API key.").StringVar(&cfg.UNAS.APIKey)
	app.Flag("unas.name", "Fallback device label when the device reports no name.").StringVar(&cfg.UNASName)
	app.Flag("unas.insecure-skip-verify", "Disable TLS verification for the UNAS.").BoolVar(&cfg.UNAS.InsecureSkipVerify)

	app.Flag("protect.url", "UniFi OS URL of the Protect console.").StringVar(&cfg.Protect.URL)
	app.Flag("protect.username", "UniFi OS local account username.").StringVar(&cfg.Protect.Username)
	app.Flag("protect.password", "UniFi OS local account password.").StringVar(&cfg.Protect.Password)
	app.Flag("protect.name", "Fallback device label when the console reports no name.").StringVar(&cfg.ProtectName)
	app.Flag("protect.insecure-skip-verify", "Disable TLS verification for the Protect console.").BoolVar(&cfg.Protect.InsecureSkipVerify)

	if _, err := app.Parse(args); err != nil {
		return nil, err
	}
	if helpRequested {
		return nil, ErrHelpRequested
	}

	// Environment fallback: applied only where the flag left the value empty,
	// so a flag always wins.
	fallback(&cfg.UNAS.URL, getenv("UNAS_URL"))
	fallback(&cfg.UNAS.APIKey, getenv("UNAS_API_KEY"))
	fallback(&cfg.UNASName, getenv("UNAS_NAME"))
	fallback(&cfg.Protect.URL, getenv("PROTECT_URL"))
	fallback(&cfg.Protect.Username, getenv("PROTECT_USERNAME"))
	fallback(&cfg.Protect.Password, getenv("PROTECT_PASSWORD"))
	fallback(&cfg.ProtectName, getenv("PROTECT_NAME"))

	// A collector runs when it is not disabled AND has somewhere to connect to,
	// so either half can be left unconfigured without a flag.
	cfg.UNASEnabled = *unasOn && cfg.UNAS.URL != ""
	cfg.ProtectEnabled = *protectOn && cfg.Protect.URL != ""

	if cfg.UNASEnabled && cfg.UNAS.APIKey == "" {
		return nil, fmt.Errorf("unas.url is set but unas.api-key is empty")
	}
	if cfg.ProtectEnabled && (cfg.Protect.Username == "" || cfg.Protect.Password == "") {
		return nil, fmt.Errorf("protect.url is set but protect.username or protect.password is empty")
	}

	return &cfg, nil
}

func fallback(dst *string, env string) {
	if *dst == "" {
		*dst = env
	}
}
