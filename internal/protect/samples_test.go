package protect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"

	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/testutil"
)

// fixtureCameraID is the sole camera's id in testdata/bootstrap.json. The
// device that produced the fixture happened to be mid-disconnect when
// captured, so the camera's baseline isConnected is already false — tests
// that need a connected/disconnected contrast flip it explicitly rather than
// relying on the fixture's captured state.
const fixtureCameraID = "fixture-cam-g4-instant"

func collectorFor(t *testing.T, bs *Bootstrap, name string) prometheus.Collector {
	t.Helper()
	var out testutil.MetricSlice
	for _, s := range Samples(bs, name) {
		m, err := s.Metric()
		if err != nil {
			t.Fatalf("sample %q did not render: %v", s.Name, err)
		}
		out = append(out, m)
	}
	return out
}

// namesIn reports every metric name Samples produced, for absence assertions.
func namesIn(bs *Bootstrap, name string) map[string]bool {
	got := map[string]bool{}
	for _, s := range Samples(bs, name) {
		got[s.Name] = true
	}
	return got
}

// testBootstrap returns a fresh decode of the fixture so mutations applied by
// one test can never leak into another.
func testBootstrap(t *testing.T) *Bootstrap {
	t.Helper()
	return FixtureBootstrap(t)
}

func cameraByID(t *testing.T, bs *Bootstrap, id string) *Camera {
	t.Helper()
	for i := range bs.Cameras {
		if bs.Cameras[i].ID == id {
			return &bs.Cameras[i]
		}
	}
	t.Fatalf("camera %q not found in fixture", id)
	return nil
}

func markCameraDisconnected(t *testing.T, bs *Bootstrap, id string) {
	t.Helper()
	cameraByID(t, bs, id).IsConnected = false
}

func clearCameraLastMotion(t *testing.T, bs *Bootstrap, id string) {
	t.Helper()
	cameraByID(t, bs, id).LastMotion = nil
}

func clearNVRName(bs *Bootstrap) {
	bs.NVR.Name = ""
}

func TestSamplesCameraExposition(t *testing.T) {
	bs := testBootstrap(t)
	c := collectorFor(t, bs, "fallback")

	// The fixture's only camera was captured mid-disconnect (isConnected:
	// false), so protect_camera_up reads 0 here. This pins the label KEYS the
	// mapper actually emits — camera/type/mac, no "id" — not a particular
	// connectivity state.
	expected := `
# HELP protect_camera_up Whether the camera is connected to Protect.
# TYPE protect_camera_up gauge
protect_camera_up{camera="Example Camera",mac="000000000010",type="UVC G4 Instant"} 0
`
	if err := promtestutil.CollectAndCompare(c, strings.NewReader(expected), "protect_camera_up"); err != nil {
		t.Fatal(err)
	}
}

func TestSamplesOmitsWifiForDisconnectedCamera(t *testing.T) {
	bs := testBootstrap(t)
	cam := cameraByID(t, bs, fixtureCameraID)

	// The fixture camera is already disconnected but still carries wifi
	// readings (signalQuality 50, signalStrength 0) from before it dropped —
	// exactly the stale-data trap the mapper guards against. Flip it
	// connected first and confirm wifi IS published in that state, so the
	// disconnected-omission check below can't pass merely because the mapper
	// never emits wifi at all.
	cam.IsConnected = true
	connected := namesIn(bs, "fallback")
	if len(connected) == 0 {
		t.Fatal("Samples returned nothing while camera connected; the checks below would pass vacuously")
	}
	if !connected["protect_camera_up"] {
		t.Fatal("protect_camera_up was not emitted at all")
	}
	for _, n := range []string{"protect_camera_wifi_signal_strength_dbm", "protect_camera_wifi_signal_quality"} {
		if !connected[n] {
			t.Fatalf("%s not published while camera connected, though the fixture carries a non-null reading", n)
		}
	}

	markCameraDisconnected(t, bs, fixtureCameraID)

	// A disconnected camera keeps reporting its last wifi numbers, and a
	// signalStrength of 0 reads as a perfect signal. Omit rather than publish.
	got := namesIn(bs, "fallback")
	if len(got) == 0 {
		t.Fatal("Samples returned nothing; the absence checks below would pass vacuously")
	}
	if !got["protect_camera_up"] {
		t.Fatal("protect_camera_up was not emitted at all")
	}
	for _, n := range []string{"protect_camera_wifi_signal_strength_dbm", "protect_camera_wifi_signal_quality"} {
		if got[n] {
			t.Errorf("%s published for a disconnected camera", n)
		}
	}
}

func TestSamplesOmitsNullTimestamps(t *testing.T) {
	// The fixture camera's lastMotion IS set (1783826090900); confirm that
	// baseline first so clearing it below demonstrates an actual transition
	// rather than asserting an absence that was already true beforehand.
	baseline := namesIn(testBootstrap(t), "fallback")
	if !baseline["protect_camera_last_motion_timestamp_seconds"] {
		t.Fatal("fixture camera has no last-motion reading; this test cannot demonstrate omission")
	}

	bs := testBootstrap(t)
	clearCameraLastMotion(t, bs, fixtureCameraID)

	// Absence must mean "never happened" — that is what lets the motion and
	// leak alerts be recency checks rather than latches that can never clear.
	got := namesIn(bs, "fallback")
	if len(got) == 0 {
		t.Fatal("Samples returned nothing; the absence check below would pass vacuously")
	}
	if !got["protect_camera_up"] {
		t.Fatal("protect_camera_up was not emitted at all")
	}
	if got["protect_camera_last_motion_timestamp_seconds"] {
		t.Error("last-motion timestamp published when the source was null")
	}
}

func TestSamplesNVRUsesDeviceNameFallback(t *testing.T) {
	bs := testBootstrap(t)
	clearNVRName(bs)

	var found bool
	for _, s := range Samples(bs, "fallback-name") {
		if s.Name == "protect_nvr_info" {
			found = true
			if s.Labels["device"] != "fallback-name" {
				t.Errorf("device label = %q, want the fallback", s.Labels["device"])
			}
		}
	}
	// Without this, the loop above matches nothing and the test passes
	// vacuously if protect_nvr_info is ever dropped or renamed.
	if !found {
		t.Fatal("protect_nvr_info was not emitted at all")
	}
}

// newBootstrapServer serves the UniFi OS login shape and the redacted
// bootstrap fixture, incrementing *logins on every POST /api/auth/login. It
// mirrors unifios' own osServer helper (internal/unifios/session_test.go),
// which client-level tests there use to drive login-count assertions.
func newBootstrapServer(t *testing.T, logins *int) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile("testdata/bootstrap.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
			*logins++
			w.Header().Set("X-CSRF-Token", "csrf-abc")
			http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: "t"})
			w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == bootstrapPath:
			w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestSourceReusesOneLogin(t *testing.T) {
	var logins int
	srv := newBootstrapServer(t, &logins)
	defer srv.Close()

	c, err := New(Config{URL: srv.URL, Username: "u", Password: "p", InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	src := &Source{Client: c, DeviceName: "fallback"}

	for i := 0; i < 3; i++ {
		if _, err := src.Collect(context.Background()); err != nil {
			t.Fatalf("collect %d: %v", i, err)
		}
	}

	// Three scrapes, one login. Rebuilding the client per scrape would make this
	// 3 — and in production, one login every scrape interval forever.
	if logins != 1 {
		t.Errorf("logins = %d across 3 collections, want 1", logins)
	}
}
