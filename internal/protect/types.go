package protect

// The Protect bootstrap payload is ~39 KB and mostly irrelevant here; only the
// fields the metric set and widget consume are declared.
//
// Optional numerics are POINTERS on purpose. Protect uses null pervasively and
// meaningfully — a null leak timestamp means "never detected", a null sensor
// reading means "this mount does not report it". Decoding into value types
// collapses all of that to 0, which is how a device that has never leaked ends
// up publishing a leak at the Unix epoch.

type Bootstrap struct {
	NVR     NVR      `json:"nvr"`
	Cameras []Camera `json:"cameras"`
	Sensors []Sensor `json:"sensors"`
	Bridges []Bridge `json:"bridges"`
}

type NVR struct {
	Name            string     `json:"name"`
	MAC             string     `json:"mac"`
	Version         string     `json:"version"`
	FirmwareVersion string     `json:"firmwareVersion"`
	MarketName      string     `json:"marketName"`
	ReleaseChannel  string     `json:"releaseChannel"`
	LastSeen        *int64     `json:"lastSeen"`
	Uptime          *int64     `json:"uptime"`
	SystemInfo      SystemInfo `json:"systemInfo"`
}

type SystemInfo struct {
	CPU      CPU      `json:"cpu"`
	Memory   Memory   `json:"memory"`
	Storage  Storage  `json:"storage"`
	UStorage UStorage `json:"ustorage"`
}

type CPU struct {
	// AverageLoad is a PERCENTAGE (21.2 means 21.2%), not a 0-1 ratio.
	AverageLoad *float64 `json:"averageLoad"`
	Temperature *float64 `json:"temperature"`
}

// Memory values are KiB, not bytes.
type Memory struct {
	Total     int64 `json:"total"`
	Available int64 `json:"available"`
	Free      int64 `json:"free"`
}

type Storage struct {
	Size        int64  `json:"size"`
	Used        int64  `json:"used"`
	Available   int64  `json:"available"`
	Capability  string `json:"capability"`
	IsRecycling bool   `json:"isRecycling"`
}

type UStorage struct {
	Disks []UStorageDisk `json:"disks"`
}

type UStorageDisk struct {
	Slot    int    `json:"slot"`
	Type    string `json:"type"`
	Model   string `json:"model"`
	Serial  string `json:"serial"`
	Size    int64  `json:"size"`
	Healthy string `json:"healthy"`
	State   string `json:"state"`
}

type Camera struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Type            string      `json:"type"`
	MAC             string      `json:"mac"`
	State           string      `json:"state"`
	IsConnected     bool        `json:"isConnected"`
	IsRecording     bool        `json:"isRecording"`
	FirmwareVersion string      `json:"firmwareVersion"`
	LastSeen        *int64      `json:"lastSeen"`
	LastMotion      *int64      `json:"lastMotion"`
	Stats           CameraStats `json:"stats"`
}

type CameraStats struct {
	Wifi    WifiStats     `json:"wifi"`
	Storage CameraStorage `json:"storage"`
}

type WifiStats struct {
	SignalQuality  *float64 `json:"signalQuality"`
	SignalStrength *float64 `json:"signalStrength"`
}

type CameraStorage struct {
	Used *int64 `json:"used"`
	// ChannelStorage is keyed by channel index ("0", "2", ...), not a scalar.
	ChannelStorage map[string]ChannelStorage `json:"channelStorage"`
}

type ChannelStorage struct {
	Rotating  RecordingSize `json:"rotating"`
	Timelapse RecordingSize `json:"timelapse"`
}

type RecordingSize struct {
	RecordingsSizeBytes       int64 `json:"recordingsSizeBytes"`
	LockedRecordingsSizeBytes int64 `json:"lockedRecordingsSizeBytes"`
}

type Sensor struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	MAC             string `json:"mac"`
	State           string `json:"state"`
	IsConnected     bool   `json:"isConnected"`
	MountType       string `json:"mountType"`
	FirmwareVersion string `json:"firmwareVersion"`
	LastSeen        *int64 `json:"lastSeen"`

	IsMotionDetected bool  `json:"isMotionDetected"`
	IsOpened         *bool `json:"isOpened"`

	LeakDetectedAt      *int64 `json:"leakDetectedAt"`
	TamperingDetectedAt *int64 `json:"tamperingDetectedAt"`
	AlarmTriggeredAt    *int64 `json:"alarmTriggeredAt"`
	MotionDetectedAt    *int64 `json:"motionDetectedAt"`

	BatteryStatus            BatteryStatus  `json:"batteryStatus"`
	BluetoothConnectionState BluetoothState `json:"bluetoothConnectionState"`
	Stats                    SensorStats    `json:"stats"`

	// FeatureFlags values are objects ({"channelCount":1}), so capability is
	// determined by KEY PRESENCE. Decoding into map[string]bool would silently
	// drop every flag.
	FeatureFlags map[string]interface{} `json:"featureFlags"`
}

type BatteryStatus struct {
	Percentage *float64 `json:"percentage"`
	IsLow      bool     `json:"isLow"`
}

type BluetoothState struct {
	SignalQuality  *float64 `json:"signalQuality"`
	SignalStrength *float64 `json:"signalStrength"`
}

type SensorStats struct {
	Temperature SensorReading `json:"temperature"`
	Humidity    SensorReading `json:"humidity"`
	Light       SensorReading `json:"light"`
}

type SensorReading struct {
	Value  *float64 `json:"value"`
	Status string   `json:"status"`
}

type Bridge struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	MAC             string `json:"mac"`
	State           string `json:"state"`
	IsConnected     bool   `json:"isConnected"`
	FirmwareVersion string `json:"firmwareVersion"`
	LastSeen        *int64 `json:"lastSeen"`
}
