package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ---------- AppMacro ----------

const amCols = `id, name, description, package_name, events_json, device_width, device_height, created_at, updated_at`

func scanAppMacro(row interface{ Scan(...any) error }) (*AppMacro, error) {
	m := &AppMacro{}
	var desc sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(&m.ID, &m.Name, &desc, &m.PackageName, &m.EventsJSON,
		&m.DeviceWidth, &m.DeviceHeight, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	if desc.Valid {
		m.Description = desc.String
	}
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		m.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
		m.UpdatedAt = t
	}
	return m, nil
}

func (db *DB) ListAppMacros(ctx context.Context) ([]*AppMacro, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+amCols+` FROM app_macros ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AppMacro
	for rows.Next() {
		m, err := scanAppMacro(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (db *DB) FindAppMacro(ctx context.Context, id int64) (*AppMacro, error) {
	row := db.QueryRowContext(ctx, `SELECT `+amCols+` FROM app_macros WHERE id=?`, id)
	m, err := scanAppMacro(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

func (db *DB) CreateAppMacro(ctx context.Context, m *AppMacro) (*AppMacro, error) {
	if m.Name == "" || m.EventsJSON == "" {
		return nil, fmt.Errorf("name and eventsJson required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `INSERT INTO app_macros
		(name, description, package_name, events_json, device_width, device_height, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Name, m.Description, m.PackageName, m.EventsJSON,
		m.DeviceWidth, m.DeviceHeight, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.FindAppMacro(ctx, id)
}

func (db *DB) UpdateAppMacro(ctx context.Context, id int64, m *AppMacro) (*AppMacro, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `UPDATE app_macros
		SET name=?, description=?, package_name=?, events_json=?,
		    device_width=?, device_height=?, updated_at=? WHERE id=?`,
		m.Name, m.Description, m.PackageName, m.EventsJSON,
		m.DeviceWidth, m.DeviceHeight, now, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return db.FindAppMacro(ctx, id)
}

func (db *DB) DeleteAppMacro(ctx context.Context, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM app_macros WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- ScheduledJob ----------

const sjCols = `id, name, description, enabled, type, server_id, device_ids, config,
	cron_expression, busy_policy, retry_count, retry_delay_seconds,
	notify_on_failure, notify_on_success, notify_webhook_url,
	last_run_at, last_run_status, next_run_at, created_at, updated_at`

func scanScheduledJob(row interface{ Scan(...any) error }) (*ScheduledJob, error) {
	s := &ScheduledJob{}
	var desc sql.NullString
	var enabled, notifyFail, notifySucc int
	var lastRunAt, nextRunAt sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(
		&s.ID, &s.Name, &desc, &enabled, &s.Type, &s.ServerID, &s.DeviceIDs, &s.Config,
		&s.CronExpression, &s.BusyPolicy, &s.RetryCount, &s.RetryDelaySeconds,
		&notifyFail, &notifySucc, &s.NotifyWebhookURL,
		&lastRunAt, &s.LastRunStatus, &nextRunAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	if desc.Valid {
		s.Description = desc.String
	}
	s.Enabled = enabled != 0
	s.NotifyOnFailure = notifyFail != 0
	s.NotifyOnSuccess = notifySucc != 0
	if lastRunAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, lastRunAt.String); err == nil {
			s.LastRunAt = sql.NullTime{Time: t, Valid: true}
		}
	}
	if nextRunAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, nextRunAt.String); err == nil {
			s.NextRunAt = sql.NullTime{Time: t, Valid: true}
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		s.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
		s.UpdatedAt = t
	}
	return s, nil
}

func (db *DB) ListScheduledJobs(ctx context.Context) ([]*ScheduledJob, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+sjCols+` FROM scheduled_jobs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ScheduledJob
	for rows.Next() {
		s, err := scanScheduledJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (db *DB) FindScheduledJob(ctx context.Context, id int64) (*ScheduledJob, error) {
	row := db.QueryRowContext(ctx, `SELECT `+sjCols+` FROM scheduled_jobs WHERE id=?`, id)
	s, err := scanScheduledJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

func (db *DB) CreateScheduledJob(ctx context.Context, s *ScheduledJob) (*ScheduledJob, error) {
	if s.Name == "" || s.Type == "" || s.CronExpression == "" {
		return nil, fmt.Errorf("name, type, cronExpression required")
	}
	if s.BusyPolicy == "" {
		s.BusyPolicy = "reject"
	}
	if s.RetryDelaySeconds == 0 {
		s.RetryDelaySeconds = 60
	}
	enabled, notifyFail, notifySucc := boolInt(s.Enabled), boolInt(s.NotifyOnFailure), boolInt(s.NotifyOnSuccess)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `INSERT INTO scheduled_jobs
		(name, description, enabled, type, server_id, device_ids, config,
		 cron_expression, busy_policy, retry_count, retry_delay_seconds,
		 notify_on_failure, notify_on_success, notify_webhook_url, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Name, s.Description, enabled, s.Type, s.ServerID, s.DeviceIDs, s.Config,
		s.CronExpression, s.BusyPolicy, s.RetryCount, s.RetryDelaySeconds,
		notifyFail, notifySucc, s.NotifyWebhookURL, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.FindScheduledJob(ctx, id)
}

func (db *DB) UpdateScheduledJob(ctx context.Context, id int64, s *ScheduledJob) (*ScheduledJob, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	enabled, notifyFail, notifySucc := boolInt(s.Enabled), boolInt(s.NotifyOnFailure), boolInt(s.NotifyOnSuccess)
	res, err := db.ExecContext(ctx, `UPDATE scheduled_jobs
		SET name=?, description=?, enabled=?, type=?, server_id=?, device_ids=?, config=?,
		    cron_expression=?, busy_policy=?, retry_count=?, retry_delay_seconds=?,
		    notify_on_failure=?, notify_on_success=?, notify_webhook_url=?, updated_at=?
		WHERE id=?`,
		s.Name, s.Description, enabled, s.Type, s.ServerID, s.DeviceIDs, s.Config,
		s.CronExpression, s.BusyPolicy, s.RetryCount, s.RetryDelaySeconds,
		notifyFail, notifySucc, s.NotifyWebhookURL, now, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return db.FindScheduledJob(ctx, id)
}

func (db *DB) DeleteScheduledJob(ctx context.Context, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM scheduled_jobs WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ToggleScheduledJobEnabled — enabled 플래그만 토글하고 next_run_at 재계산 없이 저장.
// next_run 재계산은 schedule/runner.go 가 reload 시 수행.
func (db *DB) ToggleScheduledJobEnabled(ctx context.Context, id int64, enabled bool) (*ScheduledJob, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET enabled=?, updated_at=? WHERE id=?`,
		boolInt(enabled), now, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return db.FindScheduledJob(ctx, id)
}

// UpdateScheduledJobLastRun — runner 가 fire 후 결과를 기록할 때 사용.
func (db *DB) UpdateScheduledJobLastRun(ctx context.Context, id int64, status string, nextRun *time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var nextStr any
	if nextRun != nil {
		nextStr = nextRun.UTC().Format(time.RFC3339Nano)
	}
	_, err := db.ExecContext(ctx, `UPDATE scheduled_jobs
		SET last_run_at=?, last_run_status=?, next_run_at=?, updated_at=? WHERE id=?`,
		now, status, nextStr, now, id)
	return err
}

// UpdateScheduledJobNextRun — Reload 직후 cron next-fire 시간만 기록.
// last_run_* 는 건드리지 않는다.
func (db *DB) UpdateScheduledJobNextRun(ctx context.Context, id int64, nextRun *time.Time) error {
	if nextRun == nil {
		return nil
	}
	nextStr := nextRun.UTC().Format(time.RFC3339Nano)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET next_run_at=?, updated_at=? WHERE id=?`,
		nextStr, now, id)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
