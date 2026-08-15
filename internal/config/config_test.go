package config

import (
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
