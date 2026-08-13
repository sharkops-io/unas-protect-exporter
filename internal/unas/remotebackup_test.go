package unas

import (
	"encoding/json"
	"testing"
	"time"
)

// byName indexes samples for assertion. Several metrics repeat per task, so
// this keys on name+task rather than name alone.
func byName(t *testing.T, tasks []RemoteBackupTask) map[string]map[string]float64 {
	t.Helper()
	out := map[string]map[string]float64{}
	for _, s := range RemoteBackupSamples(tasks, time.Now()) {
		if out[s.Name] == nil {
			out[s.Name] = map[string]float64{}
		}
		out[s.Name][s.Labels["task"]] = s.Value
	}
	return out
}

func TestRemoteBackupSamplesMapsTaskState(t *testing.T) {
	got := byName(t, FixtureRemoteBackupTasks())

	for _, tc := range []struct {
		metric, task string
		want         float64
	}{
		{"unas_remote_backup_enabled", "Example to Cloud", 1},
		{"unas_remote_backup_enabled", "Stale to Cloud", 0},
		{"unas_remote_backup_needs_reauth", "Example to Cloud", 0},
		{"unas_remote_backup_needs_reauth", "Stale to Cloud", 1},
		{"unas_remote_backup_last_run_error_count", "Example to Cloud", 0},
		{"unas_remote_backup_last_run_error_count", "Stale to Cloud", 1},
		// 05:00:07 -> 05:19:04
		{"unas_remote_backup_last_run_duration_seconds", "Example to Cloud", 1137},
	} {
		if v, ok := got[tc.metric][tc.task]; !ok {
			t.Errorf("%s{task=%q} not emitted", tc.metric, tc.task)
		} else if v != tc.want {
			t.Errorf("%s{task=%q} = %v, want %v", tc.metric, tc.task, v, tc.want)
		}
	}
}

// The nil-run path is the one that would otherwise publish a 1970 timestamp
// and read as catastrophically overdue.
func TestRemoteBackupSamplesOmitsRunSeriesForNeverRunTask(t *testing.T) {
	got := byName(t, FixtureRemoteBackupTasks())

	const never = "Never Run"
	for _, m := range []string{
		"unas_remote_backup_last_run_status",
		"unas_remote_backup_last_run_error_count",
		"unas_remote_backup_last_run_timestamp_seconds",
		"unas_remote_backup_last_run_duration_seconds",
		"unas_remote_backup_next_run_timestamp_seconds",
	} {
		if _, ok := got[m][never]; ok {
			t.Errorf("%s{task=%q} was emitted; a never-run task must publish no run or next-run series", m, never)
		}
	}
	// It must still publish its task-level series, or the task would be
	// invisible rather than merely un-run.
	for _, m := range []string{
		"unas_remote_backup_task_info",
		"unas_remote_backup_enabled",
		"unas_remote_backup_needs_reauth",
	} {
		if _, ok := got[m][never]; !ok {
			t.Errorf("%s{task=%q} missing; a never-run task must still be visible", m, never)
		}
	}
}

// No success/failure boolean is derived from Status: only "succeeded" has ever
// been observed from the device, so the failure and in-flight spellings are
// unknown. The status is carried as a label instead.
func TestRemoteBackupSamplesCarriesStatusAsALabelNotABoolean(t *testing.T) {
	var statuses []string
	for _, s := range RemoteBackupSamples(FixtureRemoteBackupTasks(), time.Now()) {
		if s.Name == "unas_remote_backup_last_run_status" {
			if s.Value != 1 {
				t.Errorf("last_run_status value = %v, want 1; it is an info metric", s.Value)
			}
			statuses = append(statuses, s.Labels["status"])
		}
		if s.Name == "unas_remote_backup_last_run_success" {
			t.Error("unas_remote_backup_last_run_success is emitted; deriving success from an unknown status vocabulary is exactly what this design avoids")
		}
	}
	if len(statuses) != 2 {
		t.Fatalf("got %d status samples, want 2 (the never-run task emits none)", len(statuses))
	}
}

// A run still in flight has a start and no finish. Computing a duration from
// that yields a negative number, which is worse than no sample.
func TestRemoteBackupSamplesOmitsDurationForUnfinishedRun(t *testing.T) {
	tasks := []RemoteBackupTask{{
		ID: "t1", Name: "In Flight",
		Schedule:    RemoteBackupSchedule{Enable: true},
		LastTaskRun: &RemoteBackupRun{Status: "running", StartedAt: time.Now()},
	}}
	for _, s := range RemoteBackupSamples(tasks, time.Now()) {
		switch s.Name {
		case "unas_remote_backup_last_run_duration_seconds":
			t.Error("duration emitted for a run with no finishedAt")
		case "unas_remote_backup_last_run_timestamp_seconds":
			t.Error("finish timestamp emitted for a run with no finishedAt")
		}
	}
}

// The real payload shape, trimmed. Decoding is asserted separately from
// mapping so a device-side field rename fails here with a clear cause.
func TestRemoteBackupDecodesTheDevicePayload(t *testing.T) {
	const body = `{"err":null,"type":"collection","total":1,"data":[{
      "id":"d7443f8c","name":"RustFS to Google","destinationDir":"UniFi Drive_SharkNAS",
      "sourceDirs":["/shared/rustfs"],"needReauth":false,
      "nextBackup":"2026-08-14T05:00:00-05:00",
      "remote":{"type":"googleDrive"},
      "schedule":{"enable":true,"interval":60},
      "lastTaskRun":{"status":"succeeded","errorCodes":[],
        "startedAt":"2026-08-13T05:00:06.84946-05:00",
        "finishedAt":"2026-08-13T05:19:04.008103-05:00"}}]}`

	var env remoteBackupEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data) != 1 {
		t.Fatalf("got %d tasks, want 1", len(env.Data))
	}
	got := env.Data[0]
	if got.Name != "RustFS to Google" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Remote.Type != "googleDrive" {
		t.Errorf("remote type = %q", got.Remote.Type)
	}
	if !got.Schedule.Enable {
		t.Error("schedule.enable decoded false")
	}
	if got.LastTaskRun == nil {
		t.Fatal("lastTaskRun decoded nil")
	}
	if got.LastTaskRun.Status != "succeeded" {
		t.Errorf("status = %q", got.LastTaskRun.Status)
	}
	// Offset timestamps must survive the round trip; a mis-parsed one would
	// silently shift every staleness calculation by the UTC offset.
	if d := got.LastTaskRun.FinishedAt.Sub(got.LastTaskRun.StartedAt); d < 18*time.Minute || d > 19*time.Minute {
		t.Errorf("run duration = %v, want ~18m57s", d)
	}
}

// A task list with no lastTaskRun key at all must decode to nil, not to a
// zero-valued run that would publish 1970.
func TestRemoteBackupDecodesMissingLastRunAsNil(t *testing.T) {
	var env remoteBackupEnvelope
	if err := json.Unmarshal([]byte(`{"data":[{"id":"x","name":"New"}]}`), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data[0].LastTaskRun != nil {
		t.Error("absent lastTaskRun decoded to a non-nil run")
	}
}
