package config

import (
	"errors"
	"testing"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestFlagsBeatEnv(t *testing.T) {
	cfg, err := Parse(
		[]string{"--unas.url=https://flag.example.com"},
		envFrom(map[string]string{"UNAS_URL": "https://env.example.com", "UNAS_API_KEY": "k"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UNAS.URL != "https://flag.example.com" {
		t.Errorf("url = %q, want the flag value", cfg.UNAS.URL)
	}
}

func TestEnvUsedWhenFlagAbsent(t *testing.T) {
	cfg, err := Parse(nil, envFrom(map[string]string{
		"UNAS_URL": "https://env.example.com", "UNAS_API_KEY": "k",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UNAS.APIKey != "k" {
		t.Errorf("api key = %q, want the env value", cfg.UNAS.APIKey)
	}
}

func TestCollectorDisabledWhenURLUnset(t *testing.T) {
	cfg, err := Parse(nil, envFrom(map[string]string{
		"PROTECT_URL": "https://unifi.example.com", "PROTECT_USERNAME": "u", "PROTECT_PASSWORD": "p",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UNASEnabled {
		t.Error("unas collector enabled with no URL configured")
	}
	if !cfg.ProtectEnabled {
		t.Error("protect collector not enabled despite full config")
	}
}

func TestTLSVerificationDefaultsToOn(t *testing.T) {
	cfg, err := Parse(nil, envFrom(map[string]string{"UNAS_URL": "https://x.example.com", "UNAS_API_KEY": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	// The flag is phrased negatively precisely so the zero value is the safe one.
	if cfg.UNAS.InsecureSkipVerify {
		t.Error("TLS verification defaulted to OFF; it must default to ON")
	}
}

func TestExplicitDisableOverridesConfiguredURL(t *testing.T) {
	cfg, err := Parse([]string{"--no-collector.unas"}, envFrom(map[string]string{
		"UNAS_URL": "https://x.example.com", "UNAS_API_KEY": "k",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UNASEnabled {
		t.Error("--no-collector.unas did not disable the collector")
	}
}

func TestMissingAPIKeyIsAnError(t *testing.T) {
	// A URL with no credential is a misconfiguration, not a reason to start up
	// and emit success=0 forever.
	if _, err := Parse(nil, envFrom(map[string]string{"UNAS_URL": "https://x.example.com"})); err == nil {
		t.Fatal("expected an error for a UNAS URL with no API key")
	}
}

func TestHelpReturnsDistinctSentinel(t *testing.T) {
	// --help must be distinguishable from both a real configuration error and
	// a clean, empty configuration: main() exits 0 for this one and 1 for
	// every other Parse error, including "no collectors enabled", which is
	// otherwise indistinguishable from --help once kingpin's own os.Exit is
	// disabled (see ErrHelpRequested's doc comment).
	cfg, err := Parse([]string{"--help"}, envFrom(nil))
	if cfg != nil {
		t.Errorf("cfg = %+v, want nil on --help", cfg)
	}
	if !errors.Is(err, ErrHelpRequested) {
		t.Fatalf("err = %v, want ErrHelpRequested", err)
	}
}

func TestNoFlagsIsAnOrdinaryErrorNotHelp(t *testing.T) {
	// The counterpart to TestHelpReturnsDistinctSentinel: an empty command
	// line with nothing configured must NOT be mistaken for --help. Parse
	// itself returns a zero-value Config with no error here (neither
	// collector's URL is set, so neither credential check fires); it is
	// cmd/unas-protect-exporter's "no collectors enabled" check, one layer up,
	// that turns this into a fatal error. This test just pins that Parse does
	// not itself return ErrHelpRequested for a plain empty invocation.
	_, err := Parse(nil, envFrom(nil))
	if errors.Is(err, ErrHelpRequested) {
		t.Fatal("an empty command line must not be reported as --help")
	}
}
