package config

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
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

// TestHTTPTimeoutReachesTheClients is the regression test for a flag that did
// nothing. --collector.timeout only ever bounded the context; the *http.Client
// underneath both integrations was built with a hard-wired 10s, so raising the
// collector timeout to 60s left every request still capped at 10s. The flag
// value has to arrive on the client configs for either timeout to mean what it
// says.
func TestHTTPTimeoutReachesTheClients(t *testing.T) {
	cfg, err := Parse([]string{
		"--http.timeout=45s", "--collector.timeout=90s",
		"--unas.url=https://unas.example.com", "--unas.api-key=k",
		"--protect.url=https://protect.example.com", "--protect.username=u", "--protect.password=p",
	}, envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UNAS.Timeout != 45*time.Second {
		t.Errorf("unas http timeout = %v, want 45s", cfg.UNAS.Timeout)
	}
	if cfg.Protect.Timeout != 45*time.Second {
		t.Errorf("protect http timeout = %v, want 45s", cfg.Protect.Timeout)
	}
	if cfg.Timeout != 90*time.Second {
		t.Errorf("collector timeout = %v, want 90s", cfg.Timeout)
	}
}

func TestHTTPTimeoutDefaultsToTen(t *testing.T) {
	cfg, err := Parse([]string{"--unas.url=https://unas.example.com", "--unas.api-key=k"}, envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UNAS.Timeout != 10*time.Second {
		t.Errorf("default http timeout = %v, want 10s", cfg.UNAS.Timeout)
	}
}

func TestLogFlags(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		wantLevel slog.Level
		wantJSON  bool
	}{
		{nil, slog.LevelInfo, false},
		{[]string{"--log.level=debug"}, slog.LevelDebug, false},
		{[]string{"--log.level=warn"}, slog.LevelWarn, false},
		// --log.level=error is the only mitigation available for slog.Error
		// firing once per failed scrape: a multi-hour device outage otherwise
		// writes hundreds of identical "collection failed" lines.
		{[]string{"--log.level=error", "--log.format=json"}, slog.LevelError, true},
	} {
		args := append([]string{"--unas.url=https://unas.example.com", "--unas.api-key=k"}, tc.args...)
		cfg, err := Parse(args, envFrom(nil))
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if cfg.LogLevel != tc.wantLevel {
			t.Errorf("%v: level = %v, want %v", tc.args, cfg.LogLevel, tc.wantLevel)
		}

		var buf bytes.Buffer
		log := slog.New(cfg.LogHandler(&buf))
		// Emitted at the configured level so it always survives the filter,
		// which keeps this about FORMAT.
		log.Log(context.Background(), tc.wantLevel, "hello", "k", "v")
		if gotJSON := strings.HasPrefix(buf.String(), "{"); gotJSON != tc.wantJSON {
			t.Errorf("%v: json output = %v, want %v (%q)", tc.args, gotJSON, tc.wantJSON, buf.String())
		}

		// And the level actually filters: one step below the configured level
		// must produce nothing.
		buf.Reset()
		log.Log(context.Background(), tc.wantLevel-1, "quieter")
		if buf.Len() != 0 {
			t.Errorf("%v: message below the configured level was not filtered: %q", tc.args, buf.String())
		}
	}
}

func TestLogFlagsRejectUnknownValues(t *testing.T) {
	for _, arg := range []string{"--log.level=trace", "--log.format=xml"} {
		if _, err := Parse([]string{arg}, envFrom(nil)); err == nil {
			t.Errorf("%s was accepted; enum values must be validated at parse time", arg)
		}
	}
}
