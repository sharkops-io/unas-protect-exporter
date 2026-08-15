// Package config binds this exporter's device flags. The flags every exporter
// in this family shares live in exporter-common/config, which this embeds.
package config

import (
	"fmt"

	"github.com/alecthomas/kingpin/v2"

	common "gitlab.com/sharkops/exporters/exporter-common/config"

	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/protect"
	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/unas"
)

// ErrHelpRequested is re-exported so callers need not import the shared
// package just to check one sentinel.
var ErrHelpRequested = common.ErrHelpRequested

type Config struct {
	*common.Base

	UNASEnabled bool
	UNAS        unas.Config
	UNASName    string

	// RemoteBackupEnabled gates the UNAS remote-backup collector. It is
	// separate from UNASEnabled because it needs a different credential: the
	// API key cannot read that endpoint (403 invalid auth user ID), only a
	// cookie session can. Deliberately NOT a hard requirement — see the
	// assignment in Parse.
	RemoteBackupEnabled bool

	ProtectEnabled bool
	Protect        protect.Config
	ProtectName    string
}

// Parse builds a Config from args and the environment. getenv is injected so
// the precedence rules are testable without touching the real environment.
func Parse(args []string, getenv func(string) string) (*Config, error) {
	app := kingpin.New("unas-protect-exporter", "Prometheus exporter for UniFi UNAS and UniFi Protect.")
	base := common.Register(app, common.Defaults{ListenAddress: ":9101"})

	cfg := Config{Base: base}

	unasOn := app.Flag("collector.unas", "Enable the UNAS collector.").Default("true").Bool()
	protectOn := app.Flag("collector.protect", "Enable the Protect collector.").Default("true").Bool()

	app.Flag("unas.url", "UniFi OS URL of the UNAS.").StringVar(&cfg.UNAS.URL)
	app.Flag("unas.api-key", "UNAS local API key.").StringVar(&cfg.UNAS.APIKey)
	app.Flag("unas.username", "UNAS local account username. Optional; enables the remote-backup collector, which the API key cannot authenticate.").
		StringVar(&cfg.UNAS.Username)
	app.Flag("unas.password", "UNAS local account password. Optional; see --unas.username.").
		StringVar(&cfg.UNAS.Password)
	app.Flag("unas.name", "Fallback device label when the device reports no name.").StringVar(&cfg.UNASName)
	app.Flag("unas.insecure-skip-verify", "Disable TLS verification for the UNAS.").BoolVar(&cfg.UNAS.InsecureSkipVerify)

	app.Flag("protect.url", "UniFi OS URL of the Protect console.").StringVar(&cfg.Protect.URL)
	app.Flag("protect.username", "UniFi OS local account username.").StringVar(&cfg.Protect.Username)
	app.Flag("protect.password", "UniFi OS local account password.").StringVar(&cfg.Protect.Password)
	app.Flag("protect.name", "Fallback device label when the console reports no name.").StringVar(&cfg.ProtectName)
	app.Flag("protect.insecure-skip-verify", "Disable TLS verification for the Protect console.").BoolVar(&cfg.Protect.InsecureSkipVerify)

	if err := common.Parse(app, base, args); err != nil {
		return nil, err
	}

	cfg.UNAS.Timeout = base.HTTPTimeout
	cfg.Protect.Timeout = base.HTTPTimeout

	// Environment fallback: applied only where the flag left the value empty,
	// so a flag always wins.
	common.Fallback(&cfg.UNAS.URL, getenv("UNAS_URL"))
	common.Fallback(&cfg.UNAS.APIKey, getenv("UNAS_API_KEY"))
	common.Fallback(&cfg.UNAS.Username, getenv("UNAS_USERNAME"))
	common.Fallback(&cfg.UNAS.Password, getenv("UNAS_PASSWORD"))
	common.Fallback(&cfg.UNASName, getenv("UNAS_NAME"))
	common.Fallback(&cfg.Protect.URL, getenv("PROTECT_URL"))
	common.Fallback(&cfg.Protect.Username, getenv("PROTECT_USERNAME"))
	common.Fallback(&cfg.Protect.Password, getenv("PROTECT_PASSWORD"))
	common.Fallback(&cfg.ProtectName, getenv("PROTECT_NAME"))

	// A collector runs when it is not disabled AND has somewhere to connect
	// to, so either half can be left unconfigured without a flag.
	cfg.UNASEnabled = *unasOn && cfg.UNAS.URL != ""
	cfg.ProtectEnabled = *protectOn && cfg.Protect.URL != ""

	// Opt-in by credential presence, NOT an error when absent. This exporter
	// is public and every existing deployment has only an API key; making the
	// session credentials mandatory would turn an upgrade into an outage for
	// them.
	cfg.RemoteBackupEnabled = cfg.UNASEnabled && cfg.UNAS.Username != "" && cfg.UNAS.Password != ""

	if cfg.UNASEnabled && cfg.UNAS.APIKey == "" {
		return nil, fmt.Errorf("unas.url is set but unas.api-key is empty")
	}
	// One half of a credential pair is a typo, not a choice, and silently
	// disabling the collector would hide it.
	if cfg.UNASEnabled && (cfg.UNAS.Username == "") != (cfg.UNAS.Password == "") {
		return nil, fmt.Errorf("unas.username and unas.password must be set together")
	}
	if cfg.ProtectEnabled && (cfg.Protect.Username == "" || cfg.Protect.Password == "") {
		return nil, fmt.Errorf("protect.url is set but protect.username or protect.password is empty")
	}

	return &cfg, nil
}
