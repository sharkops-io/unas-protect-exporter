package protect

import (
	_ "embed"
	"encoding/json"
)

// fixtureBootstrapJSON is embedded at build time relative to this source
// file, rather than read at runtime relative to the working directory: the
// dashboard tests in another package (a different directory) call
// FixtureBootstrap too, and a relative os.ReadFile would resolve against
// their working directory, not this package's.
//
//go:embed testdata/bootstrap.json
var fixtureBootstrapJSON []byte

// Fataler is the one method FixtureBootstrap needs. *testing.T satisfies it,
// and so does a tiny shim in cmd-line tools, which keeps `testing` out of the
// non-test build.
type Fataler interface {
	Fatalf(format string, args ...any)
}

// FixtureBootstrap decodes a redacted bootstrap payload carrying one NVR, one
// camera, one sensor and one bridge — one of each kind, so every mapper branch
// and therefore every label is exercised.
//
// The captured device never saw a leak, so the raw payload's sensor carries
// leakDetectedAt: null — faithfully, which is exactly what
// TestDecodeSensorNullsSurvive (client_test.go) exists to pin. That test reads
// testdata/bootstrap.json directly, so it is unaffected by the mutation below:
// this function sets a representative LeakDetectedAt on the decoded copy it
// returns, so protect_sensor_leak_detected_timestamp_seconds — a real,
// conditionally-emitted metric the dashboard legitimately queries — is
// exercised here too. Leaving it nil would silently drop that metric from the
// derived "emitted" set the dashboard test checks against.
func FixtureBootstrap(t Fataler) *Bootstrap {
	var bs Bootstrap
	if err := json.Unmarshal(fixtureBootstrapJSON, &bs); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if len(bs.Sensors) == 0 {
		return &bs
	}
	s := &bs.Sensors[0]
	if s.LeakDetectedAt == nil {
		leak := int64(1785300000000)
		s.LeakDetectedAt = &leak
	}
	// The captured leak sensor never opened, tampered, alarmed or moved, and
	// its temperature/humidity/light readings are genuinely unavailable on
	// that mount type (see TestDecodeSensorNullsSurvive, which pins the RAW
	// decode of testdata/bootstrap.json — untouched by this function — as all
	// nil/"unknown"). Those are real, conditionally-emitted metrics with no
	// other fixture to exercise them, so set them here on the decoded copy
	// only, the same way LeakDetectedAt is set above, purely so every mapper
	// branch produces a sample at least once for metricdoc and the dashboard
	// label-set test.
	if s.IsOpened == nil {
		opened := false
		s.IsOpened = &opened
	}
	ts := int64(1785300000000)
	if s.TamperingDetectedAt == nil {
		s.TamperingDetectedAt = &ts
	}
	if s.AlarmTriggeredAt == nil {
		s.AlarmTriggeredAt = &ts
	}
	if s.MotionDetectedAt == nil {
		s.MotionDetectedAt = &ts
	}
	if s.Stats.Temperature.Value == nil {
		v := 21.5
		s.Stats.Temperature.Value = &v
		s.Stats.Temperature.Status = "good"
	}
	if s.Stats.Humidity.Value == nil {
		v := 45.0
		s.Stats.Humidity.Value = &v
		s.Stats.Humidity.Status = "good"
	}
	if s.Stats.Light.Value == nil {
		v := 120.0
		s.Stats.Light.Value = &v
		s.Stats.Light.Status = "good"
	}
	return &bs
}
