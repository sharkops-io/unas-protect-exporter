package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sharkops-io/unas-protect-exporter/internal/config"
	"github.com/sharkops-io/unas-protect-exporter/internal/protect"
)

// The end-to-end tests below are the only ones that exercise config → clients
// → registry → the /metrics HTTP surface together, which is the one place all
// of this exporter's seams meet. Every other package tests one seam in
// isolation, and that is exactly how the handler's ErrorHandling default —
// under which a single bad series answered /metrics with an empty HTTP 500 —
// survived a whole build's worth of green per-package tests.

// unasServer serves the two endpoints unas.Client.GetStorage fetches. The
// storage payload is inline JSON rather than a Go fixture on purpose: this
// test is about what comes off the wire.
func unasServer(t *testing.T) *httptest.Server {
	t.Helper()
	const storage = `{
	  "pools": [{"id":"pool-uuid-1","status":"healthy","capacity":1000,"usage":400,
	             "dataScrubbing":{"status":"idle","progress":0},
	             "raidGroups":[{"id":"raid-group-1","currentProtection":2,"expectedProtection":2}]}],
	  "disks": [{"slotId":"1","serial":"REDACTED1","model":"EXAMPLE-16TB","type":"HDD",
	             "state":"optimal","healthScore":5,"temperature":45,"powerOnHours":100}]
	}`
	const deviceInfo = `{"name":"example-unas","model":"EXAMPLE","version":"1.0.0",
	                     "firmwareVersion":"1.2.3","startupTime":"2026-01-01T00:00:00Z",
	                     "cpu":{"currentload":12.5,"temperature":40},
	                     "memory":{"total":8000000,"free":4000000,"available":5000000}}`

	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/drive/api/v2/storage", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") == "" {
			t.Error("storage request carried no X-API-Key")
		}
		io.WriteString(w, storage)
	})
	mux.HandleFunc("/proxy/drive/api/v2/systems/device-info", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, deviceInfo)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// protectServer serves the UniFi OS login and the Protect bootstrap, the
// latter straight from the package's redacted capture. When broken is true it
// answers the bootstrap with a 500, standing in for an unreachable NVR.
func protectServer(t *testing.T, broken bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-CSRF-Token", "test-token")
		http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: "test-cookie", Path: "/"})
		io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/proxy/protect/api/bootstrap", func(w http.ResponseWriter, _ *http.Request) {
		if broken {
			http.Error(w, "nvr is on fire", http.StatusInternalServerError)
			return
		}
		w.Write(marshalBootstrap(t, protect.FixtureBootstrap(t)))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// marshalBootstrap re-encodes a decoded fixture so it can be served over the
// wire. Round-tripping through the package's own types keeps only the fields
// the mappers actually read, which is all these tests assert on.
func marshalBootstrap(t *testing.T, bs *protect.Bootstrap) []byte {
	t.Helper()
	b, err := json.Marshal(bs)
	if err != nil {
		t.Fatalf("marshal bootstrap: %v", err)
	}
	return b
}

// exporter stands the whole thing up the way main does — config, clients,
// registry, handler — and serves it over a real listener. extra collectors are
// registered alongside the configured ones, which is how a test injects a
// failure mode no httptest device server can produce.
func exporter(t *testing.T, args []string, extra ...prometheus.Collector) *httptest.Server {
	t.Helper()
	cfg, err := config.Parse(args, func(string) string { return "" })
	if err != nil {
		t.Fatalf("config.Parse: %v", err)
	}
	reg, err := buildRegistry(cfg)
	if err != nil {
		t.Fatalf("buildRegistry: %v", err)
	}
	for _, c := range extra {
		reg.MustRegister(c)
	}

	mux := http.NewServeMux()
	mux.Handle(cfg.TelemetryPath, metricsHandler(reg))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// get performs one real GET against the exporter's telemetry path.
func get(t *testing.T, srv *httptest.Server) (int, string) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(body)
}

// scrape is the single-shot form: stand up an exporter and read it once.
func scrape(t *testing.T, args []string) (int, string) {
	t.Helper()
	return get(t, exporter(t, args))
}

// panicCollector is a registered collector that blows up mid-Collect, standing
// in for a nil-dereference in a mapper on an unforeseen payload shape.
// client_golang recovers such a panic into a gather ERROR rather than letting
// it kill the process — which is precisely the input that HTTPErrorOnError
// turns into an empty HTTP 500.
type panicCollector struct{}

func (panicCollector) Describe(chan<- *prometheus.Desc) {}
func (panicCollector) Collect(chan<- prometheus.Metric) { panic("mapper dereferenced a nil field") }

func TestMetricsEndpointServesBothNamespaces(t *testing.T) {
	unasSrv := unasServer(t)
	protectSrv := protectServer(t, false)

	status, body := scrape(t, []string{
		"--unas.url=" + unasSrv.URL, "--unas.api-key=test-key", "--unas.name=example-unas",
		"--protect.url=" + protectSrv.URL, "--protect.username=u", "--protect.password=p",
		"--protect.name=example-nvr",
	})
	if status != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200\nbody:\n%s", status, body)
	}

	for _, want := range []string{
		"unas_disk_state{",
		"unas_pool_status{",
		"unas_info{",
		"unas_scrape_collector_success 1",
		"protect_nvr_info{",
		"protect_camera_up{",
		"protect_scrape_collector_success 1",
		// The runtime families are on this endpoint too, and are part of what
		// an HTTP 500 on one bad series would have thrown away.
		"go_goroutines",
		"process_",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics is missing %q", want)
		}
	}
}

// TestMetricsEndpointIsolatesAFailingCollector asserts the isolation property
// for the ordinary failure: a Protect console answering 500 must cost the
// Protect device series and nothing else — HTTP 200, every unas_* series still
// served, and protect_scrape_collector_success reading 0 so the
// collector-health alert can actually fire.
//
// This is the path the Collector already handles by retracting its samples, so
// no gather error arises and the handler's ErrorHandling never comes into it;
// the two tests below are the ones that exercise that.
func TestMetricsEndpointIsolatesAFailingCollector(t *testing.T) {
	unasSrv := unasServer(t)
	protectSrv := protectServer(t, true)

	status, body := scrape(t, []string{
		"--unas.url=" + unasSrv.URL, "--unas.api-key=test-key",
		"--protect.url=" + protectSrv.URL, "--protect.username=u", "--protect.password=p",
	})
	if status != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200 despite a failing protect collector\nbody:\n%s", status, body)
	}
	for _, want := range []string{
		"unas_disk_state{",
		"unas_pool_status{",
		"unas_scrape_collector_success 1",
		"protect_scrape_collector_success 0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics is missing %q while the protect collector was failing", want)
		}
	}
	if strings.Contains(body, "protect_nvr_info{") {
		t.Error("protect device series were published despite a failed collection")
	}
}

// TestMetricsEndpointSurvivesADuplicateSeries drives finding 1's realistic
// trigger all the way through HTTP: two UniFi Protect cameras sharing a name
// produce two identical protect_camera_recording_enabled series inside ONE
// payload, because that metric is keyed on the camera name alone and Protect
// does not enforce unique names.
//
// The scrape is done TWICE on purpose. promhttp_metric_handler_errors_total is
// itself served from this registry, so its value in any response is the count
// as of the PREVIOUS gather; a single scrape can never show that the gather it
// is reporting on went wrong. The second response is what proves the first
// gather was clean — which is the assertion that distinguishes "the collector
// deduped the series" from "the registry rejected it and ContinueOnError
// quietly served the rest".
func TestMetricsEndpointSurvivesADuplicateSeries(t *testing.T) {
	unasSrv := unasServer(t)

	bs := protect.FixtureBootstrap(t)
	if len(bs.Cameras) == 0 {
		t.Fatal("fixture has no cameras to duplicate")
	}
	// Same name, different MAC — precisely the shape a real second camera
	// adopted with a duplicate name has.
	twin := bs.Cameras[0]
	twin.MAC = "000000000002"
	bs.Cameras = append(bs.Cameras, twin)

	protectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			w.Header().Set("X-CSRF-Token", "test-token")
			io.WriteString(w, `{}`)
		case "/proxy/protect/api/bootstrap":
			w.Write(marshalBootstrap(t, bs))
		default:
			http.NotFound(w, r)
		}
	}))
	defer protectSrv.Close()

	srv := exporter(t, []string{
		"--unas.url=" + unasSrv.URL, "--unas.api-key=test-key",
		"--protect.url=" + protectSrv.URL, "--protect.username=u", "--protect.password=p",
	})
	if status, body := get(t, srv); status != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200 despite a duplicate series\nbody:\n%s", status, body)
	}
	status, body := get(t, srv)
	if status != http.StatusOK {
		t.Fatalf("second GET /metrics = %d, want 200\nbody:\n%s", status, body)
	}

	for _, want := range []string{
		"unas_disk_state{",
		"unas_scrape_collector_success 1",
		"protect_scrape_collector_success 1",
		"protect_camera_recording_enabled{",
		// No gather error was recorded for the first scrape: the duplicate was
		// dropped by the collector, never reaching the registry.
		`promhttp_metric_handler_errors_total{cause="gathering"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics is missing %q on a payload with a duplicate camera name", want)
		}
	}
	// The duplicate is dropped, not both copies and not the whole family.
	if n := strings.Count(body, "protect_camera_recording_enabled{"); n != 1 {
		t.Errorf("protect_camera_recording_enabled series = %d, want exactly 1", n)
	}
	// protect_camera_up carries a mac label, so the twin is a DISTINCT series
	// there and must survive — the dedupe drops duplicate series, not
	// duplicate names.
	if n := strings.Count(body, "protect_camera_up{"); n != 2 {
		t.Errorf("protect_camera_up series = %d, want 2 (distinct macs)", n)
	}
}

// TestMetricsEndpointSurvivesAPanickingCollector is the other half of finding
// 1, and the one that pins promhttp.ContinueOnError specifically.
//
// A panic inside one collector reaches the handler as a gather error. Under
// the zero-value HTTPErrorOnError that answered /metrics with an empty HTTP
// 500 — losing the healthy device's entire namespace, the Go and process
// families, and BOTH _scrape_collector_success gauges, so the collector-health
// alerts in examples/rules.yaml could not fire at the moment they were needed.
// ContinueOnError serves everything that gathered cleanly, with the failure
// recorded on promhttp_metric_handler_errors_total instead.
func TestMetricsEndpointSurvivesAPanickingCollector(t *testing.T) {
	unasSrv := unasServer(t)

	srv := exporter(t, []string{
		"--unas.url=" + unasSrv.URL, "--unas.api-key=test-key",
	}, panicCollector{})

	if status, body := get(t, srv); status != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200 despite a panicking collector\nbody:\n%s", status, body)
	}
	status, body := get(t, srv)
	if status != http.StatusOK {
		t.Fatalf("second GET /metrics = %d, want 200\nbody:\n%s", status, body)
	}

	for _, want := range []string{
		"unas_disk_state{",
		"unas_pool_status{",
		"unas_scrape_collector_success 1",
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics is missing %q: a panic in another collector took it down too", want)
		}
	}
	// And the panic is not swept under the rug — it is counted, so an operator
	// can alert on it.
	if strings.Contains(body, `promhttp_metric_handler_errors_total{cause="gathering"} 0`) {
		t.Error("the panic was not recorded on promhttp_metric_handler_errors_total")
	}
}
