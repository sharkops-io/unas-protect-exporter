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
	st.DeviceInfo.Status = "STATE_RUNNING"
	st.DeviceInfo.SFPAggregation = false
	// BOTH interface shapes are required by the "every shape" contract: the
	// connected one exercises the link-speed sample, and the disconnected one
	// exercises the omission path for an empty LinkSpeed. Dropping either
	// silently stops covering half of parseLinkSpeed.
	st.DeviceInfo.NetworkInterfaces = []NetworkInterface{
		{Interface: "ethernet", InterfaceName: "enp0s1", Connected: false, MaxSpeed: "GbE", LinkSpeed: ""},
		{Interface: "sfp+", InterfaceName: "enp0s2", Connected: true, MaxSpeed: "10 GbE", LinkSpeed: "10 GbE"},
	}
	st.NetworkIO = NetworkIO{ReceiveKBPS: 36.35, TransmitKBPS: 26.48}
	st.HasNetworkIO = true
	return st
}

// FixtureRemoteBackupTasks returns a representative remote-backup task list.
// Like FixtureStorage it lives in non-test code so the dashboard tests, which
// are in another package, derive the emitted metric set from the mappers
// themselves rather than from a list that rots.
//
// Three tasks, chosen to cover every branch in RemoteBackupSamples:
//
//   - a healthy scheduled task with a completed run,
//   - one needing re-auth with a failed run carrying error codes,
//   - one that has NEVER run (LastTaskRun nil) and has no next run scheduled,
//     which must publish task-level series and no run series at all.
//
// Dropping the third would silently stop covering the nil-run path, which is
// the one that would otherwise publish a 1970 timestamp.
func FixtureRemoteBackupTasks() []RemoteBackupTask {
	started := time.Date(2026, 8, 13, 5, 0, 7, 0, time.UTC)
	finished := time.Date(2026, 8, 13, 5, 19, 4, 0, time.UTC)
	return []RemoteBackupTask{
		{
			ID: "task-uuid-1", Name: "Example to Cloud",
			DestinationDir: "ExampleDrive_ExampleNAS",
			SourceDirs:     []string{"/shared/example"},
			NeedReauth:     false,
			NextBackup:     time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC),
			Remote:         RemoteBackupRemote{Type: "googleDrive"},
			Schedule:       RemoteBackupSchedule{Enable: true},
			LastTaskRun: &RemoteBackupRun{
				Status: "succeeded", StartedAt: started, FinishedAt: finished,
				ErrorCodes: []string{},
			},
		},
		{
			ID: "task-uuid-2", Name: "Stale to Cloud",
			DestinationDir: "ExampleDrive_ExampleNAS",
			SourceDirs:     []string{"/shared/stale"},
			NeedReauth:     true,
			NextBackup:     time.Date(2026, 8, 14, 6, 0, 0, 0, time.UTC),
			Remote:         RemoteBackupRemote{Type: "googleDrive"},
			Schedule:       RemoteBackupSchedule{Enable: false},
			LastTaskRun: &RemoteBackupRun{
				Status: "failed", StartedAt: started, FinishedAt: finished,
				ErrorCodes: []string{"ERR_EXAMPLE"},
			},
		},
		{
			ID: "task-uuid-3", Name: "Never Run",
			DestinationDir: "ExampleDrive_ExampleNAS",
			SourceDirs:     []string{"/shared/new"},
			Remote:         RemoteBackupRemote{Type: "googleDrive"},
			Schedule:       RemoteBackupSchedule{Enable: true},
		},
	}
}
