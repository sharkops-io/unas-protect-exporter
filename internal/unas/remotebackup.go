package unas

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gitlab.com/sharkops/exporters/exporter-common/metric"
	"gitlab.com/sharkops/exporters/unas-protect-exporter/internal/unifios"
)

// remoteBackupPath serves the UNAS's "remote backup" jobs — the scheduled
// syncs to an external destination such as Google Drive.
//
// Note the v1: the storage and device-info endpoints this package already uses
// are v2, and there is no v2 equivalent of this one. Do not "modernise" the
// path.
const remoteBackupPath = "/proxy/drive/api/v1/remote-backup/tasks"

// RemoteBackupClient reads remote-backup tasks.
//
// THIS DOES NOT USE THE API KEY, and that is not an oversight. The static
// X-API-KEY that serves /api/v2/storage returns
//
//	HTTP 403 {"err":{"msg":"invalid auth user ID"}}
//
// on this endpoint (measured 2026-08-13). The device key carries no user
// identity, and remote-backup tasks are per-user records — every task in the
// payload has a `createdBy`. So this needs a UniFi OS cookie session, exactly
// like the Protect side, against the SAME host the API-key client talks to.
//
// One device reached two ways looks like an inconsistency worth cleaning up.
// It cannot be: neither credential can reach the other's endpoints.
type RemoteBackupClient struct {
	host    string
	session *unifios.Session
}

// NewRemoteBackup builds a session-authenticated client. Username and password
// are a LOCAL device account; UniFi OS offers no read-only role on the UNAS
// Pro, so this is necessarily a full-admin credential used for a single GET.
func NewRemoteBackup(cfg Config) (*RemoteBackupClient, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("unas url is required")
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("unas username and password are required for remote-backup collection")
	}
	host := strings.TrimRight(cfg.URL, "/")
	session, err := unifios.New(host, cfg.Username, cfg.Password,
		unifios.Options{InsecureSkipVerify: cfg.InsecureSkipVerify, Timeout: cfg.Timeout})
	if err != nil {
		return nil, err
	}
	return &RemoteBackupClient{host: host, session: session}, nil
}

// remoteBackupEnvelope is the collection wrapper the endpoint returns.
type remoteBackupEnvelope struct {
	Data []RemoteBackupTask `json:"data"`
}

// RemoteBackupTask is one scheduled sync to an external destination.
type RemoteBackupTask struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	DestinationDir string   `json:"destinationDir"`
	SourceDirs     []string `json:"sourceDirs"`
	// NeedReauth is the single most valuable field here. An expired OAuth
	// grant is how an offsite backup dies silently: the task stays configured,
	// the schedule stays enabled, and nothing ships.
	NeedReauth bool `json:"needReauth"`
	// NextBackup is the scheduled next run. Zero when the device omits it.
	NextBackup time.Time            `json:"nextBackup"`
	Remote     RemoteBackupRemote   `json:"remote"`
	Schedule   RemoteBackupSchedule `json:"schedule"`
	// LastTaskRun is nil for a task that has never run. Callers must check it
	// rather than publishing a zero-valued run.
	LastTaskRun *RemoteBackupRun `json:"lastTaskRun"`
}

type RemoteBackupRemote struct {
	Type string `json:"type"`
}

type RemoteBackupSchedule struct {
	Enable bool `json:"enable"`
}

type RemoteBackupRun struct {
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	// ErrorCodes is empty on a clean run. This is the failure signal to rely
	// on: it is a count, not a string, so alerting on it does not depend on
	// knowing how the device spells its failure statuses. Only "succeeded" has
	// been observed in the wild, so the spellings of the failure and in-flight
	// states are genuinely unknown — see RemoteBackupSamples.
	ErrorCodes []string `json:"errorCodes"`
}

// GetRemoteBackupTasks fetches and decodes the remote-backup task list.
func (c *RemoteBackupClient) GetRemoteBackupTasks(ctx context.Context) ([]RemoteBackupTask, error) {
	body, err := c.session.Get(ctx, c.host+remoteBackupPath)
	if err != nil {
		return nil, fmt.Errorf("unas remote-backup tasks: %w", err)
	}
	var env remoteBackupEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("unas remote-backup tasks: decode: %w", err)
	}
	return env.Data, nil
}

// RemoteBackupSamples maps the task list onto the metric set. Pure, with now
// injected, so the mapping is testable without a clock or a device.
//
// NO SUCCESS/FAILURE BOOLEAN IS DERIVED FROM Status, deliberately. Only
// "succeeded" has ever been observed from this device, so any mapping of other
// values would be a guess — and the failure mode of guessing wrong is the
// worst available: an in-flight run whose status is some unknown spelling of
// "running" would publish success=0 and fire a false alert every night, or
// worse, an unrecognised genuine failure would publish success=1 and stay
// silent. Instead:
//
//   - unas_remote_backup_last_run_status carries the raw string as a label,
//     following the unas_pool_status / unas_disk_state convention already used
//     in this package for device-defined enums.
//   - unas_remote_backup_last_run_error_count is the numeric failure signal.
//     It is a length, so it needs no knowledge of the device's vocabulary.
//
// Alert on the error count; use the status label to read what happened.
func RemoteBackupSamples(tasks []RemoteBackupTask, now time.Time) []metric.Sample {
	out := make([]metric.Sample, 0, len(tasks)*8)

	for _, t := range tasks {
		l := map[string]string{"task": t.Name, "task_id": t.ID}

		out = append(out,
			metric.Gauge("unas_remote_backup_task_info",
				"UNAS remote-backup task; always 1, read the labels.", 1,
				map[string]string{
					"task": t.Name, "task_id": t.ID,
					"type": t.Remote.Type, "destination": t.DestinationDir,
				}),
			metric.Gauge("unas_remote_backup_enabled",
				"Whether the UNAS remote-backup task's schedule is enabled.",
				boolToFloat(t.Schedule.Enable), l),
			metric.Gauge("unas_remote_backup_needs_reauth",
				"Whether the UNAS remote-backup task needs its remote credentials re-authorised. 1 means the destination is no longer being written to.",
				boolToFloat(t.NeedReauth), l),
		)

		// Omitted rather than zeroed when the device reports no next run: a 0
		// here would render as 1970 and read as catastrophically overdue. Same
		// reasoning as unas_uptime_seconds in samples.go — a gap is honest, a
		// fabricated zero is not.
		if !t.NextBackup.IsZero() {
			out = append(out, metric.Gauge("unas_remote_backup_next_run_timestamp_seconds",
				"Unix timestamp of the UNAS remote-backup task's next scheduled run.",
				float64(t.NextBackup.Unix()), l))
		}

		// A task that has never run publishes no run series at all, for the
		// same reason. Its absence is visible via unas_remote_backup_task_info
		// existing without a matching last-run series.
		if t.LastTaskRun == nil {
			continue
		}
		r := t.LastTaskRun

		out = append(out,
			metric.Gauge("unas_remote_backup_last_run_status",
				"UNAS remote-backup task's last run status; always 1, read the status label.", 1,
				map[string]string{"task": t.Name, "task_id": t.ID, "status": r.Status}),
			metric.Gauge("unas_remote_backup_last_run_error_count",
				"Number of error codes on the UNAS remote-backup task's last run. 0 is a clean run.",
				float64(len(r.ErrorCodes)), l),
		)

		if !r.FinishedAt.IsZero() {
			out = append(out, metric.Gauge("unas_remote_backup_last_run_timestamp_seconds",
				"Unix timestamp when the UNAS remote-backup task's last run finished.",
				float64(r.FinishedAt.Unix()), l))
		}
		// Duration needs BOTH ends. A run still in flight has a start and no
		// finish, which would otherwise compute a negative duration.
		if !r.FinishedAt.IsZero() && !r.StartedAt.IsZero() {
			out = append(out, metric.Gauge("unas_remote_backup_last_run_duration_seconds",
				"Seconds the UNAS remote-backup task's last run took.",
				r.FinishedAt.Sub(r.StartedAt).Seconds(), l))
		}
	}

	return out
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// RemoteBackupSource adapts a RemoteBackupClient to collector.Source.
//
// Its Namespace is deliberately NOT "unas". exporter-common's collector builds
// <ns>_scrape_collector_success from it, and that Collector is unchecked, so
// two Sources sharing a namespace would emit label-identical gauges that the
// registry silently accepts and Prometheus reports as a duplicate-series
// gather error. The alternative — collector.NewWithLabels on both — would
// change the label set of the EXISTING unas_scrape_collector_success series
// and break the alerts and dashboard already keyed on it.
type RemoteBackupSource struct {
	Client *RemoteBackupClient
}

func (s *RemoteBackupSource) Namespace() string { return "unas_remote_backup" }

func (s *RemoteBackupSource) Collect(ctx context.Context) ([]metric.Sample, error) {
	tasks, err := s.Client.GetRemoteBackupTasks(ctx)
	if err != nil {
		return nil, err
	}
	return RemoteBackupSamples(tasks, time.Now()), nil
}
