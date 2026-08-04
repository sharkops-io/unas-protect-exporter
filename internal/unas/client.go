package unas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/httpx"
)

// Config is the exporter's UNAS connection config. Unlike another-app' version
// this has no JSON tags and no pointer-bool: that pointer existed so a config
// row saved before the field was added still meant "verify". Flags have no
// such history, and the flag itself is phrased negatively, so a plain bool
// whose zero value means "verify" is correct here.
type Config struct {
	URL                string
	APIKey             string
	InsecureSkipVerify bool
}

const (
	storagePath    = "/proxy/drive/api/v2/storage"
	deviceInfoPath = "/proxy/drive/api/v2/systems/device-info"
)

// Storage is the typed view of the storage + device-info payloads used by the
// metrics bridge.
// Numeric fields are float64 because every one of them becomes a metric value.
type Storage struct {
	Pools []Pool `json:"pools"`
	Disks []Disk `json:"disks"`
	// DeviceInfo's json tag is inert for the /storage response (the device does
	// not return that key); it mirrors the key GetStorage merges device-info
	// under, so both fetches populate recognisably the same shape.
	DeviceInfo DeviceInfo `json:"device_info"`
	// HasDeviceInfo reports whether the best-effort device-info fetch actually
	// produced a payload. Callers MUST check it before publishing anything off
	// DeviceInfo: a zero-valued DeviceInfo is indistinguishable from a real
	// reading of 0 °C / 0 bytes, and Prometheus never retracts a recorded sample.
	HasDeviceInfo bool `json:"-"`
}

type Pool struct {
	ID            string        `json:"id"`
	Number        int           `json:"number"`
	Type          string        `json:"type"`
	PreferLevel   string        `json:"preferLevel"`
	Status        string        `json:"status"`
	Capacity      float64       `json:"capacity"`
	Usage         float64       `json:"usage"`
	RaidGroups    []RaidGroup   `json:"raidGroups"`
	DataScrubbing DataScrubbing `json:"dataScrubbing"`
}

type RaidGroup struct {
	ID                 string  `json:"id"`
	Number             int     `json:"number"`
	ConfigLevel        string  `json:"configLevel"`
	CurrentLevel       string  `json:"currentLevel"`
	CurrentProtection  float64 `json:"currentProtection"`
	ExpectedProtection float64 `json:"expectedProtection"`
	Progress           float64 `json:"progress"`
}

// DataScrubbing.Progress is a percentage (0-100) reported while Status is
// "inProgress"; the pool's own Status reads "scrubbing" for the same period.
type DataScrubbing struct {
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
}

type Disk struct {
	SlotID                   string  `json:"slotId"`
	Serial                   string  `json:"serial"`
	Model                    string  `json:"model"`
	Type                     string  `json:"type"`
	State                    string  `json:"state"`
	PoolID                   string  `json:"poolId"`
	Size                     float64 `json:"size"`
	HealthScore              float64 `json:"healthScore"`
	Temperature              float64 `json:"temperature"`
	PowerOnHours             float64 `json:"powerOnHours"`
	BadSectorCount           float64 `json:"badSectorCount"`
	UncorrectableSectorCount float64 `json:"uncorrectableSectorCount"`
	SmartReadErrorCount      float64 `json:"smartReadErrorCount"`
	ReadErrorRate            float64 `json:"readErrorRate"`
	ReadKBPS                 float64 `json:"readKBPS"`
	WriteKBPS                float64 `json:"writeKBPS"`
}

// StartupTime is the device's boot timestamp, decoded leniently: an empty or
// unparseable value leaves the zero time instead of failing the surrounding
// decode. time.Time's own UnmarshalJSON would error, and encoding/json reports
// that error for the whole DeviceInfo — which would cost every CPU, memory and
// build-info reading because one timestamp was malformed.
type StartupTime struct{ time.Time }

func (s *StartupTime) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil || raw == "" {
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		s.Time = parsed
	}
	return nil
}

// DeviceInfo memory values are KiB, not bytes.
type DeviceInfo struct {
	Name            string      `json:"name"`
	Model           string      `json:"model"`
	Version         string      `json:"version"`
	FirmwareVersion string      `json:"firmwareVersion"`
	StartupTime     StartupTime `json:"startupTime"`
	CPU             struct {
		CurrentLoad float64 `json:"currentload"`
		Temperature float64 `json:"temperature"`
	} `json:"cpu"`
	Memory struct {
		Total     float64 `json:"total"`
		Free      float64 `json:"free"`
		Available float64 `json:"available"`
	} `json:"memory"`
}

type Client struct {
	host       string
	apiKey     string
	httpClient *http.Client
}

// New builds a UNAS client. Verification is by HOSTNAME: a config pointing at
// a bare IP will fail against the device's real certificate, by design.
func New(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("unas url is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("unas api key is required")
	}
	return &Client{
		host:       strings.TrimRight(cfg.URL, "/"),
		apiKey:     cfg.APIKey,
		httpClient: httpx.New(httpx.Options{InsecureSkipVerify: cfg.InsecureSkipVerify}),
	}, nil
}

func (c *Client) buildURL(path string) string {
	return fmt.Sprintf("%s%s", c.host, path)
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	headers := map[string]string{"Accept": "application/json"}
	if c.apiKey != "" {
		headers["X-API-Key"] = c.apiKey
	}

	b, err := httpx.GetBytes(ctx, c.httpClient, c.buildURL(path), headers)
	if err != nil {
		var he *httpx.HTTPError
		if errors.As(err, &he) && (he.StatusCode == http.StatusUnauthorized || he.StatusCode == http.StatusForbidden) {
			return nil, fmt.Errorf("unauthorized: check your API key")
		}
		return nil, err
	}
	return b, nil
}

// GetStorage fetches the storage and device-info endpoints, decoded into
// typed structs for the metrics bridge. Device info is best-effort: a failure
// there leaves DeviceInfo zero-valued rather than losing the disks and pools,
// and clears HasDeviceInfo so callers can tell "not fetched" from "really zero".
func (c *Client) GetStorage(ctx context.Context) (*Storage, error) {
	b, err := c.get(ctx, storagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get storage data: %w", err)
	}

	var st Storage
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("failed to parse storage json: %w", err)
	}

	// Deliberately: a decode failure here is treated exactly like a fetch
	// failure. A partially-decoded DeviceInfo is as misleading as an absent one,
	// so HasDeviceInfo is set only when both steps succeed. The error itself is
	// still dropped — device-info is best-effort and must not fail the storage
	// read — but it can no longer masquerade as real readings of zero.
	if devBytes, devErr := c.get(ctx, deviceInfoPath); devErr == nil {
		if err := json.Unmarshal(devBytes, &st.DeviceInfo); err == nil {
			st.HasDeviceInfo = true
		} else {
			st.DeviceInfo = DeviceInfo{}
		}
	}

	return &st, nil
}
