package protect

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

// loadFixture decodes the redacted 2026-08-02 capture of the live device.
func loadFixture(t *testing.T) *Bootstrap {
	t.Helper()
	b, err := os.ReadFile("testdata/bootstrap.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var bs Bootstrap
	if err := json.Unmarshal(b, &bs); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return &bs
}

func TestDecodeNVR(t *testing.T) {
	bs := loadFixture(t)
	si := bs.NVR.SystemInfo

	if si.CPU.AverageLoad == nil || *si.CPU.AverageLoad != 21.2 {
		t.Errorf("cpu averageLoad = %v, want 21.2", si.CPU.AverageLoad)
	}
	// Memory is reported in KiB, not bytes — the mapping layer multiplies.
	if si.Memory.Total != 4040828 {
		t.Errorf("memory total = %d, want 4040828 (KiB)", si.Memory.Total)
	}
	if si.Storage.Size != 983429763072 || si.Storage.Capability != "ok" {
		t.Errorf("storage = %+v", si.Storage)
	}
	if len(si.UStorage.Disks) != 1 {
		t.Fatalf("ustorage disks = %d, want 1", len(si.UStorage.Disks))
	}
	d := si.UStorage.Disks[0]
	if d.Slot != 1 || d.Healthy != "good" || d.State != "normal" {
		t.Errorf("disk = %+v", d)
	}
	// Confirmed null on this platform; must not decode as 0.
	if bs.NVR.Uptime != nil {
		t.Errorf("nvr.uptime = %v, want nil", bs.NVR.Uptime)
	}
	if bs.NVR.MarketName != "UDM Pro" {
		t.Errorf("marketName = %q", bs.NVR.MarketName)
	}
}

func TestDecodeCameraKeepsDisconnectedState(t *testing.T) {
	bs := loadFixture(t)
	if len(bs.Cameras) != 1 {
		t.Fatalf("cameras = %d, want 1", len(bs.Cameras))
	}
	c := bs.Cameras[0]
	if c.IsConnected {
		t.Error("camera should decode as disconnected")
	}
	// isRecording is configured intent and reads true on a disconnected camera.
	if !c.IsRecording {
		t.Error("isRecording should decode as true")
	}
	if c.State != "DISCONNECTED" {
		t.Errorf("state = %q", c.State)
	}
	if c.LastSeen == nil || *c.LastSeen != 1783825797928 {
		t.Errorf("lastSeen = %v", c.LastSeen)
	}
	if c.Stats.Storage.Used != nil {
		t.Errorf("stats.storage.used = %v, want nil", c.Stats.Storage.Used)
	}
	ch, ok := c.Stats.Storage.ChannelStorage["0"]
	if !ok {
		t.Fatal(`missing channelStorage["0"]`)
	}
	if ch.Rotating.RecordingsSizeBytes != 28991029248 {
		t.Errorf("channel 0 rotating = %d", ch.Rotating.RecordingsSizeBytes)
	}
}

func TestDecodeSensorNullsSurvive(t *testing.T) {
	bs := loadFixture(t)
	s := bs.Sensors[0]

	if s.LeakDetectedAt != nil || s.TamperingDetectedAt != nil ||
		s.AlarmTriggeredAt != nil || s.MotionDetectedAt != nil {
		t.Error("event timestamps must decode as nil, not 0 — absence means never")
	}
	if s.IsOpened != nil {
		t.Errorf("isOpened = %v, want nil (not an entry mount)", s.IsOpened)
	}
	if s.BatteryStatus.Percentage == nil || *s.BatteryStatus.Percentage != 97 {
		t.Errorf("battery = %v", s.BatteryStatus.Percentage)
	}
	if s.BluetoothConnectionState.SignalStrength == nil || *s.BluetoothConnectionState.SignalStrength != -83 {
		t.Errorf("bt strength = %v", s.BluetoothConnectionState.SignalStrength)
	}
	// All three optional readings are unavailable on a leak mount.
	for name, r := range map[string]SensorReading{
		"temperature": s.Stats.Temperature,
		"humidity":    s.Stats.Humidity,
		"light":       s.Stats.Light,
	} {
		if r.Value != nil {
			t.Errorf("%s value = %v, want nil", name, r.Value)
		}
		if r.Status != "unknown" {
			t.Errorf("%s status = %q, want unknown", name, r.Status)
		}
	}
	// featureFlags values are objects; capability is key presence.
	for _, want := range []string{"temperature", "humidity", "light", "waterLeak", "motion", "open"} {
		if _, ok := s.FeatureFlags[want]; !ok {
			t.Errorf("featureFlags missing %q", want)
		}
	}
}

func TestDecodeBridges(t *testing.T) {
	bs := loadFixture(t)
	if len(bs.Bridges) != 2 {
		t.Fatalf("bridges = %d, want 2", len(bs.Bridges))
	}
	var bridge1 *Bridge
	for i := range bs.Bridges {
		if bs.Bridges[i].Name == "Example Bridge 1" {
			bridge1 = &bs.Bridges[i]
		}
	}
	if bridge1 == nil {
		t.Fatal("Example Bridge 1 not found")
	}
	if bridge1.IsConnected || bridge1.State != "CONNECTING" {
		t.Errorf("Example Bridge 1 = %+v, want disconnected/CONNECTING", bridge1)
	}
}

func TestNewRequiresURLAndCredentials(t *testing.T) {
	if _, err := New(Config{Username: "u", Password: "p"}); err == nil {
		t.Error("expected an error without a url")
	}
	if _, err := New(Config{URL: "https://x", Username: "u"}); err == nil {
		t.Error("expected an error without a password")
	}
	if _, err := New(Config{URL: "https://x", Username: "u", Password: "p"}); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

// TestNewSkipsVerificationOnlyWhenExplicitlyDisabled asserts on the actual
// http.Client.Transport the session ends up with, so an inverted
// InsecureSkipVerify passed into unifios.New would be caught here rather than
// only failing silently against a real cert.
func TestNewSkipsVerificationOnlyWhenExplicitlyDisabled(t *testing.T) {
	// Default config: verification ON, so no custom Transport is installed.
	c, err := New(Config{URL: "https://x", Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if c.session.HTTPClient().Transport != nil {
		t.Error("expected verification on (nil Transport) when InsecureSkipVerify is false")
	}

	// Explicitly disabled: the insecure transport is installed.
	c2, err := New(Config{URL: "https://x", Username: "u", Password: "p", InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := c2.session.HTTPClient().Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("expected the insecure transport when InsecureSkipVerify is true")
	}
}

// TestNewAppliesTheRequestTimeout pins that Config.Timeout reaches the session's
// HTTP client. Before --http.timeout existed nothing set it, and every Protect
// request was capped at httpx.DefaultTimeout regardless of --collector.timeout.
func TestNewAppliesTheRequestTimeout(t *testing.T) {
	c, err := New(Config{URL: "https://protect.example.com", Username: "u", Password: "p",
		Timeout: 42 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.session.HTTPClient().Timeout; got != 42*time.Second {
		t.Errorf("client timeout = %v, want 42s", got)
	}
}
