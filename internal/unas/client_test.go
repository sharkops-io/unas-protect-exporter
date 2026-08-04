package unas

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/httpx"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := New(Config{URL: srv.URL, APIKey: "test-key", InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestNew_Config(t *testing.T) {
	for _, cfg := range []Config{
		{},
		{APIKey: "k"},
		{URL: "http://x"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) succeeded, want error", cfg)
		}
	}
}

func TestGetStorage_ErrorPaths(t *testing.T) {
	t.Run("401 is reported as an api key problem", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		_, err := c.GetStorage(context.Background())
		if err == nil {
			t.Fatal("expected an error for a 401 response")
		}
		if !strings.Contains(err.Error(), "API key") {
			t.Errorf("error = %q, want it to point at the API key", err)
		}
	})

	t.Run("malformed storage body", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`not json`))
		})
		if _, err := c.GetStorage(context.Background()); err == nil {
			t.Error("expected an error for an unparseable body")
		}
	})
}

func TestGetStorage_DecodesLivePayloadShape(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/storage"):
			w.Write([]byte(testStoragePayload))
		case strings.HasSuffix(r.URL.Path, "/device-info"):
			w.Write([]byte(testDeviceInfoPayload))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	st, err := c.GetStorage(context.Background())
	if err != nil {
		t.Fatalf("GetStorage: %v", err)
	}

	if len(st.Disks) != 2 {
		t.Fatalf("got %d disks, want 2", len(st.Disks))
	}
	d := st.Disks[1]
	if d.SlotID != "6" || d.Serial != "REDACTED2" {
		t.Errorf("disk 2 = slot %q serial %q, want slot 6 serial REDACTED2", d.SlotID, d.Serial)
	}
	if d.UncorrectableSectorCount != 1 {
		t.Errorf("uncorrectableSectorCount = %v, want 1", d.UncorrectableSectorCount)
	}
	if d.Temperature != 45 || d.HealthScore != 5 || d.State != "optimal" {
		t.Errorf("disk 2 temp/health/state = %v/%v/%q", d.Temperature, d.HealthScore, d.State)
	}
	if d.PowerOnHours != 11875 {
		t.Errorf("powerOnHours = %v, want 11875", d.PowerOnHours)
	}

	if len(st.Pools) != 1 {
		t.Fatalf("got %d pools, want 1", len(st.Pools))
	}
	p := st.Pools[0]
	if p.Status != "scrubbing" {
		t.Errorf("pool status = %q, want scrubbing", p.Status)
	}
	if p.Capacity != 79960856330240 || p.Usage != 26826967351296 {
		t.Errorf("pool capacity/usage = %v/%v", p.Capacity, p.Usage)
	}
	if p.DataScrubbing.Progress != 38 || p.DataScrubbing.Status != "inProgress" {
		t.Errorf("scrub = %v%% %q", p.DataScrubbing.Progress, p.DataScrubbing.Status)
	}
	if len(p.RaidGroups) != 1 || p.RaidGroups[0].CurrentProtection != 2 || p.RaidGroups[0].ExpectedProtection != 2 {
		t.Errorf("raid groups = %+v", p.RaidGroups)
	}

	if st.DeviceInfo.Model != "UNASPRO" || st.DeviceInfo.FirmwareVersion != "5.1.27" || st.DeviceInfo.Version != "4.4.7" {
		t.Errorf("device info = %+v", st.DeviceInfo)
	}
	if st.DeviceInfo.CPU.Temperature != 66 || st.DeviceInfo.CPU.CurrentLoad != 0.069 {
		t.Errorf("cpu = %+v", st.DeviceInfo.CPU)
	}
	if st.DeviceInfo.Memory.Total != 8272256 {
		t.Errorf("memory total = %v KiB, want 8272256", st.DeviceInfo.Memory.Total)
	}
	if st.DeviceInfo.StartupTime.IsZero() {
		t.Error("startupTime did not parse")
	}
	if !st.HasDeviceInfo {
		t.Error("HasDeviceInfo = false after a successful device-info fetch")
	}
}

func TestGetStorage_SurvivesMissingDeviceInfo(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/storage") {
			w.Write([]byte(testStoragePayload))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})

	st, err := c.GetStorage(context.Background())
	if err != nil {
		t.Fatalf("GetStorage: %v", err)
	}
	if len(st.Disks) != 2 {
		t.Errorf("disks lost when device-info failed: %d", len(st.Disks))
	}
	if len(st.Pools) != 1 {
		t.Errorf("pools lost when device-info failed: %d", len(st.Pools))
	}
	if st.Pools[0].Status != "scrubbing" {
		t.Errorf("pool status corrupted: %q, want scrubbing", st.Pools[0].Status)
	}
	if st.DeviceInfo.Model != "" {
		t.Errorf("device info should be zero-valued, got %+v", st.DeviceInfo)
	}
	// A zero DeviceInfo reads as 0 °C / 0 bytes, so callers need to be able to
	// tell "never fetched" from "genuinely zero".
	if st.HasDeviceInfo {
		t.Error("HasDeviceInfo = true although the device-info fetch returned 500")
	}
}

// An unparseable device-info body is treated exactly like a failed fetch: a
// half-decoded DeviceInfo is as misleading as an absent one.
func TestGetStorage_MalformedDeviceInfoIsNotDeviceInfo(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/storage") {
			w.Write([]byte(testStoragePayload))
			return
		}
		w.Write([]byte(`{"model":"UNASPRO","cpu":`)) // truncated mid-object
	})

	st, err := c.GetStorage(context.Background())
	if err != nil {
		t.Fatalf("GetStorage: %v", err)
	}
	if st.HasDeviceInfo {
		t.Error("HasDeviceInfo = true although the device-info body did not parse")
	}
	if st.DeviceInfo.Model != "" {
		t.Errorf("a partial decode was retained: %+v", st.DeviceInfo)
	}
	if len(st.Disks) != 2 {
		t.Errorf("disks lost to a malformed device-info: %d", len(st.Disks))
	}
}

// A bad timestamp must cost only the uptime series. time.Time's UnmarshalJSON
// errors on an unparseable value and encoding/json reports that for the whole
// object, which would otherwise take CPU, memory and build info down with it.
func TestGetStorage_BadStartupTimeKeepsOtherDeviceFields(t *testing.T) {
	for _, raw := range []string{`""`, `"not-a-timestamp"`, `"2026-07-23 11:31:44"`} {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/storage") {
				w.Write([]byte(testStoragePayload))
				return
			}
			w.Write([]byte(`{"model":"UNASPRO","firmwareVersion":"5.1.27","version":"4.4.7",
				"startupTime":` + raw + `,
				"cpu":{"currentload":0.069,"temperature":66},
				"memory":{"total":8272256,"available":2915712}}`))
		})

		st, err := c.GetStorage(context.Background())
		if err != nil {
			t.Fatalf("startupTime %s: GetStorage: %v", raw, err)
		}
		if !st.HasDeviceInfo {
			t.Errorf("startupTime %s: device info discarded over one bad timestamp", raw)
		}
		if st.DeviceInfo.CPU.Temperature != 66 || st.DeviceInfo.Memory.Total != 8272256 {
			t.Errorf("startupTime %s: readings lost: %+v", raw, st.DeviceInfo)
		}
		if st.DeviceInfo.Model != "UNASPRO" {
			t.Errorf("startupTime %s: model lost: %q", raw, st.DeviceInfo.Model)
		}
		if !st.DeviceInfo.StartupTime.IsZero() {
			t.Errorf("startupTime %s: parsed to %v, want the zero time", raw, st.DeviceInfo.StartupTime)
		}
	}
}

// Absent cpu/memory keys decode to zero without an error, so the device block is
// still published — with genuine zeros. Documented rather than guarded: the live
// device always reports both, and a per-field presence model would be a much
// larger change than the payload justifies.
func TestGetStorage_MissingCPUAndMemoryKeys(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/storage") {
			w.Write([]byte(testStoragePayload))
			return
		}
		w.Write([]byte(`{"model":"UNASPRO","startupTime":"2026-07-23T11:31:44Z"}`))
	})

	st, err := c.GetStorage(context.Background())
	if err != nil {
		t.Fatalf("GetStorage: %v", err)
	}
	if !st.HasDeviceInfo {
		t.Error("HasDeviceInfo = false although device-info parsed cleanly")
	}
	if st.DeviceInfo.CPU.Temperature != 0 || st.DeviceInfo.Memory.Total != 0 {
		t.Errorf("absent keys should decode to zero, got %+v", st.DeviceInfo)
	}
	if st.DeviceInfo.StartupTime.IsZero() {
		t.Error("a valid startupTime alongside absent cpu/memory should still parse")
	}
}

// Trimmed from a live ExampleNAS response: two of seven disks (slot 1 healthy,
// slot 6 with the one uncorrectable sector) and the single RAID6 pool
// mid-scrub. Network interfaces are omitted (MAC/IP). Serials are synthetic.
const testStoragePayload = `{
  "cacheSlots": [],
  "expansions": null,
  "pools": [{
    "activeRaidGroupId": "5cc2acea:2045f42a:01ff4b80:f53a5062",
    "capacity": 79960856330240,
    "dataScrubbing": {"estimate": 85545, "progress": 38, "status": "inProgress"},
    "id": "926b3588-3a07-4b42-a20d-4fa047828b44",
    "initializingStatus": "successful",
    "number": 1,
    "preferLevel": "raid6",
    "raidGroups": [{
      "configLevel": "raid6", "currentLevel": "raid6",
      "currentProtection": 2, "expectedProtection": 2,
      "id": "5cc2acea:2045f42a:01ff4b80:f53a5062", "number": 1, "progress": 38
    }],
    "status": "scrubbing",
    "type": "none",
    "usage": 26826967351296
  }],
  "disks": [
    {"ata": "ACS-4", "badSectorCount": 0, "firmware": "SN04", "healthScore": 5,
     "model": "ST16000NM001G-2KK103", "poolId": "926b3588-3a07-4b42-a20d-4fa047828b44",
     "powerOnHours": 11874, "readErrorRate": 36427417, "readKBPS": 245760, "rpm": 7200,
     "serial": "REDACTED1", "size": 16000900661248, "slotId": "1", "smartReadErrorCount": 0,
     "state": "optimal", "temperature": 45, "type": "HDD",
     "uncorrectableSectorCount": 0, "writeKBPS": 0},
    {"ata": "ACS-4", "badSectorCount": 0, "firmware": "SN04", "healthScore": 5,
     "model": "ST16000NM001G-2KK103", "poolId": "926b3588-3a07-4b42-a20d-4fa047828b44",
     "powerOnHours": 11875, "readErrorRate": 41912633, "readKBPS": 230400, "rpm": 7200,
     "serial": "REDACTED2", "size": 16000900661248, "slotId": "6", "smartReadErrorCount": 0,
     "state": "optimal", "temperature": 45, "type": "HDD",
     "uncorrectableSectorCount": 1, "writeKBPS": 0}
  ]
}`

const testDeviceInfoPayload = `{
  "cpu": {"currentload": 0.069, "temperature": 66},
  "firmwareVersion": "5.1.27",
  "memory": {"available": 2915712, "free": 1112000, "total": 8272256},
  "model": "UNASPRO",
  "name": "ExampleNAS",
  "sfpAggregation": false,
  "startupTime": "2026-07-23T11:31:44Z",
  "status": "STATE_RUNNING",
  "usbs": null,
  "version": "4.4.7"
}`

func TestNewInsecureSkipVerify(t *testing.T) {
	// Zero value (false) means verify: no custom Transport installed.
	c, err := New(Config{URL: "https://unas.example.com", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if c.httpClient.Transport != nil {
		t.Error("expected verification on (nil Transport) when InsecureSkipVerify is false")
	}

	// Explicitly disabled: the insecure transport is installed.
	c2, err := New(Config{URL: "https://unas.example.com", APIKey: "k", InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := c2.httpClient.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("expected the insecure transport when InsecureSkipVerify is true")
	}
}

// TestNewAppliesTheRequestTimeout pins the plumbing that made --http.timeout
// mean anything. Config.Timeout was previously never set, so httpx.New always
// fell back to its 10s default and a larger --collector.timeout could not
// actually give a slow NAS more time.
func TestNewAppliesTheRequestTimeout(t *testing.T) {
	c, err := New(Config{URL: "https://unas.example.com", APIKey: "k", Timeout: 42 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.httpClient.Timeout; got != 42*time.Second {
		t.Errorf("client timeout = %v, want 42s", got)
	}

	def, err := New(Config{URL: "https://unas.example.com", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if got := def.httpClient.Timeout; got != httpx.DefaultTimeout {
		t.Errorf("default client timeout = %v, want %v", got, httpx.DefaultTimeout)
	}
}
