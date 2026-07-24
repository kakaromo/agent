package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const execCols = `id, job_id, server_id, server_name, type, tool, job_name, device_ids,
	state, config, result_summary, scheduled_job_id, retry_attempt, error_message,
	started_at, completed_at, created_at,
	trace_raw_key, trace_raw_format, trace_raw_size, trace_raw_uploaded_at,
	trace_parquet_keys, trace_parsed_at, trace_parse_state, trace_parse_error,
	workload_note, trace_jobs`

func scanExec(row interface{ Scan(...any) error }) (*JobExecution, error) {
	e := &JobExecution{}
	var startedAt, completedAt, createdAt, traceRawUploadedAt, traceParsedAt sql.NullString
	err := row.Scan(
		&e.ID, &e.JobID, &e.ServerID, &e.ServerName, &e.Type, &e.Tool, &e.JobName, &e.DeviceIDs,
		&e.State, &e.Config, &e.ResultSummary, &e.ScheduledJobID, &e.RetryAttempt, &e.ErrorMessage,
		&startedAt, &completedAt, &createdAt,
		&e.TraceRawKey, &e.TraceRawFormat, &e.TraceRawSize, &traceRawUploadedAt,
		&e.TraceParquetKeys, &traceParsedAt, &e.TraceParseState, &e.TraceParseError,
		&e.WorkloadNote, &e.TraceJobs,
	)
	if err != nil {
		return nil, err
	}
	if startedAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, startedAt.String); err == nil {
			e.StartedAt = sql.NullTime{Time: t, Valid: true}
		}
	}
	if completedAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, completedAt.String); err == nil {
			e.CompletedAt = sql.NullTime{Time: t, Valid: true}
		}
	}
	if createdAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, createdAt.String); err == nil {
			e.CreatedAt = t
		}
	}
	if traceRawUploadedAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, traceRawUploadedAt.String); err == nil {
			e.TraceRawUploadedAt = sql.NullTime{Time: t, Valid: true}
		}
	}
	if traceParsedAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, traceParsedAt.String); err == nil {
			e.TraceParsedAt = sql.NullTime{Time: t, Valid: true}
		}
	}
	return e, nil
}

// SaveJobExecution — 신규 잡 시작 시 호출. job_id 가 이미 있으면 ON CONFLICT IGNORE.
func (db *DB) SaveJobExecution(ctx context.Context, e *JobExecution) (*JobExecution, error) {
	if e.JobID == "" || e.Type == "" {
		return nil, fmt.Errorf("jobId and type required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if e.State == "" {
		e.State = "running"
	}
	const q = `INSERT INTO job_executions
		(job_id, server_id, server_name, type, tool, job_name, device_ids,
		 state, config, result_summary, scheduled_job_id, retry_attempt, error_message,
		 started_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id) DO NOTHING`
	_, err := db.ExecContext(ctx, q,
		e.JobID, e.ServerID, e.ServerName, e.Type, e.Tool, e.JobName, e.DeviceIDs,
		e.State, e.Config, e.ResultSummary, e.ScheduledJobID, e.RetryAttempt, e.ErrorMessage,
		now, now,
	)
	if err != nil {
		return nil, err
	}
	return db.FindJobExecutionByJobID(ctx, e.JobID)
}

// UpdateJobExecutionState — 진행률 SSE 콜백 / 잡 종료 시 호출.
func (db *DB) UpdateJobExecutionState(ctx context.Context, jobID, state, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var (
		q    = `UPDATE job_executions SET state=?, error_message=COALESCE(?, error_message)`
		args = []any{state, nullableString(errMsg)}
	)
	// 종료 상태면 completed_at 설정.
	if state == "completed" || state == "failed" || state == "partially_failed" || state == "cancelled" {
		q += `, completed_at=?`
		args = append(args, now)
	}
	q += ` WHERE job_id=?`
	args = append(args, jobID)
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateJobExecutionResultSummary — 최종 결과 metric JSON 저장.
func (db *DB) UpdateJobExecutionResultSummary(ctx context.Context, jobID, resultJSON string) error {
	res, err := db.ExecContext(ctx, `UPDATE job_executions SET result_summary=? WHERE job_id=?`, resultJSON, jobID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateJobExecutionWorkloadNote — job 상세 워크로드 컨텍스트 메모 저장/수정.
// 빈 문자열이면 NULL 로 저장해 규칙 자동 해석으로 되돌린다.
func (db *DB) UpdateJobExecutionWorkloadNote(ctx context.Context, jobID, note string) error {
	res, err := db.ExecContext(ctx, `UPDATE job_executions SET workload_note=? WHERE job_id=?`,
		nullableString(note), jobID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateJobExecutionTraceJobs — 잡 종료 시 trace job 매핑 JSON 을 영속화.
// 빈 문자열이면 갱신하지 않는다 (기존 값 보존).
func (db *DB) UpdateJobExecutionTraceJobs(ctx context.Context, jobID, traceJobsJSON string) error {
	if traceJobsJSON == "" {
		return nil
	}
	res, err := db.ExecContext(ctx, `UPDATE job_executions SET trace_jobs=? WHERE job_id=?`,
		traceJobsJSON, jobID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) FindJobExecutionByJobID(ctx context.Context, jobID string) (*JobExecution, error) {
	row := db.QueryRowContext(ctx, `SELECT `+execCols+` FROM job_executions WHERE job_id=?`, jobID)
	e, err := scanExec(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

func (db *DB) FindJobExecution(ctx context.Context, id int64) (*JobExecution, error) {
	row := db.QueryRowContext(ctx, `SELECT `+execCols+` FROM job_executions WHERE id=?`, id)
	e, err := scanExec(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

// JobExecutionFilter — portal /agent/executions 페이지 검색 파라미터.
type JobExecutionFilter struct {
	ServerID *int64
	Type     string
	State    string
	Limit    int
	Offset   int
}

// ListJobExecutions — 페이징/필터링. created_at DESC.
func (db *DB) ListJobExecutions(ctx context.Context, f JobExecutionFilter) ([]*JobExecution, int, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	var (
		wheres []string
		args   []any
	)
	if f.ServerID != nil {
		wheres = append(wheres, "server_id=?")
		args = append(args, *f.ServerID)
	}
	if f.Type != "" {
		wheres = append(wheres, "type=?")
		args = append(args, f.Type)
	}
	if f.State != "" {
		wheres = append(wheres, "state=?")
		args = append(args, f.State)
	}
	where := ""
	if len(wheres) > 0 {
		where = " WHERE " + strings.Join(wheres, " AND ")
	}

	// total count
	var total int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_executions"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := db.QueryContext(ctx,
		"SELECT "+execCols+" FROM job_executions"+where+" ORDER BY created_at DESC LIMIT ? OFFSET ?",
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*JobExecution
	for rows.Next() {
		e, err := scanExec(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// DeleteJobExecution — id 기준 삭제 (잡 이력 정리용).
func (db *DB) DeleteJobExecution(ctx context.Context, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM job_executions WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateTraceArchiveMeta — Archive 업로드 결과 메타 저장 (Phase 6 에서 사용).
type TraceArchiveMeta struct {
	RawKey         string
	RawFormat      string
	RawSize        int64
	ParquetKeys    string // JSON: {"ufs":[...], "block":[...]}
	ParseState     string
	ParseError     string
}

func (db *DB) UpdateTraceArchiveMeta(ctx context.Context, jobID string, meta TraceArchiveMeta) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.ExecContext(ctx, `UPDATE job_executions SET
		trace_raw_key=?, trace_raw_format=?, trace_raw_size=?,
		trace_raw_uploaded_at=?, trace_parquet_keys=?, trace_parse_state=?,
		trace_parse_error=COALESCE(?, trace_parse_error)
		WHERE job_id=?`,
		nullableString(meta.RawKey), nullableString(meta.RawFormat),
		nullableInt64(meta.RawSize), now,
		nullableString(meta.ParquetKeys), nullableString(meta.ParseState),
		nullableString(meta.ParseError), jobID)
	return err
}

// ExecutionStats — /agent/executions/stats 요약 (총/완료/실패).
type ExecutionStats struct {
	Total     int     `json:"total"`
	Completed int     `json:"completed"`
	Failed    int     `json:"failed"`
	Running   int     `json:"running"`
	SuccessRate float64 `json:"successRate"`
}

func (db *DB) GetExecutionStats(ctx context.Context, serverID *int64) (*ExecutionStats, error) {
	var (
		where string
		args  []any
	)
	if serverID != nil {
		where = " WHERE server_id=?"
		args = append(args, *serverID)
	}
	s := &ExecutionStats{}
	err := db.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN state='completed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN state='failed' OR state='partially_failed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN state='running' OR state='queued' OR state='pushing_tools' OR state='collecting' THEN 1 ELSE 0 END), 0)
		FROM job_executions`+where, args...).Scan(&s.Total, &s.Completed, &s.Failed, &s.Running)
	if err != nil {
		return nil, err
	}
	if s.Total > 0 {
		s.SuccessRate = float64(s.Completed) / float64(s.Total)
	}
	return s, nil
}

// MarkStaleRunningAsFailed — agent 부팅 시 호출. 재시작 직전 agent 메모리에서 사라진 잡들의
// DB state 가 'running'/'queued'/'pushing_tools'/'collecting'/'reparsing' 로 남아 있는 것을
// 모두 'failed' + completed_at=now 로 일괄 정리한다.
//
// portal Spring 은 잡 SSE/polling 으로 자연 정리되지만 standalone 은 매번 메모리 휘발이라
// 부팅 hook 이 필요하다. 반환값은 정리된 row 수.
func (db *DB) MarkStaleRunningAsFailed(ctx context.Context, reason string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `UPDATE job_executions
		SET state='failed',
		    error_message=COALESCE(error_message, ?),
		    completed_at=?
		WHERE state IN ('running','queued','pushing_tools','collecting','reparsing')`,
		reason, now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt64(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
