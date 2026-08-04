package unas

import "time"

// FixtureStorage returns a representative Storage payload with one disk, one
// pool, one RAID group and device info present. It lives in non-test code so
// the dashboard tests — which are in another package — can derive the emitted
// metric set from the mappers themselves rather than from a list that rots.
//
// Every label-bearing shape must stay represented here: the dashboard test
// only sees labels the fixture exercises, so dropping the RAID group would
// silently stop checking the raid_group label.
func FixtureStorage() *Storage {
	st := &Storage{
		Disks: []Disk{{
			SlotID: "1", Serial: "REDACTED1", Model: "EXAMPLE-16TB", Type: "HDD",
			State: "optimal", Size: 16000900661248, HealthScore: 5, Temperature: 45,
			PowerOnHours: 11874, BadSectorCount: 0, UncorrectableSectorCount: 0,
			SmartReadErrorCount: 0, ReadErrorRate: 36427417, ReadKBPS: 245760, WriteKBPS: 0,
		}},
		Pools: []Pool{{
			ID: "pool-uuid-1", Number: 1, PreferLevel: "raid6", Status: "scrubbing",
			Capacity: 79960856330240, Usage: 26826967351296,
			DataScrubbing: DataScrubbing{Status: "inProgress", Progress: 38},
			RaidGroups: []RaidGroup{{
				ID: "raid-group-1", Number: 1, ConfigLevel: "raid6", CurrentLevel: "raid6",
				CurrentProtection: 2, ExpectedProtection: 2, Progress: 38,
			}},
		}},
		HasDeviceInfo: true,
	}
	st.DeviceInfo.Name = "ExampleNAS"
	st.DeviceInfo.Model = "UNASPRO"
	st.DeviceInfo.Version = "4.4.7"
	st.DeviceInfo.FirmwareVersion = "5.1.27"
	st.DeviceInfo.CPU.CurrentLoad = 0.069
	st.DeviceInfo.CPU.Temperature = 66
	st.DeviceInfo.Memory.Total = 8272256
	st.DeviceInfo.Memory.Available = 2915712
	// A non-zero StartupTime is part of that same "every shape" contract:
	// unas_uptime_seconds is gated on it (see samples.go), so a fixture that
	// left it zero would silently stop covering that metric for the dashboard
	// test. TestSamplesOmitsUptimeWithoutStartupTime zeroes it explicitly on
	// its own copy to exercise the omission path instead.
	st.DeviceInfo.StartupTime = StartupTime{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	return st
}
