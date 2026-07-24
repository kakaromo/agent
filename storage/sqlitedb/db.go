// Package sqlitedb 는 standalone agent 의 SQLite 영속화 레이어다.
//
// portal Spring + PostgreSQL 의 portal_* 테이블 스키마를 SQLite 로 마이그레이션한다.
// pure Go (modernc.org/sqlite) 라 CGO 불필요 → cross-compile 그대로.
//
// 7 테이블: agent_servers, job_executions, benchmark_presets, iotest_presets,
//          scenario_templates, app_macros, scheduled_jobs.
package sqlitedb

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB 는 thin wrapper. 다른 패키지에서 *sql.DB 그대로 접근 가능.
type DB struct {
	*sql.DB
	Path string
}

// Open 은 path 의 SQLite 파일을 열고 스키마를 마이그레이션한다.
// 디렉토리가 없으면 0700 으로 생성.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir db parent: %w", err)
	}
	// modernc/sqlite DSN — _pragma=foreign_keys(1) 로 FK 활성화 + busy_timeout 으로 동시성 안정.
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// 단일 standalone 프로세스 라 connection pool 작게.
	conn.SetMaxOpenConns(4)
	conn.SetMaxIdleConns(2)
	conn.SetConnMaxLifetime(time.Hour)

	db := &DB{DB: conn, Path: path}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

// migrate 는 7 테이블을 idempotent 하게 생성한다 (CREATE TABLE IF NOT EXISTS).
func (db *DB) migrate() error {
	stmts := []string{
		// AgentServer — portal portal_agent_servers
		`CREATE TABLE IF NOT EXISTS agent_servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			host TEXT NOT NULL,
			port INTEGER NOT NULL DEFAULT 50051,
			enabled INTEGER NOT NULL DEFAULT 1,
			description TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,

		// JobExecution — portal portal_job_executions
		`CREATE TABLE IF NOT EXISTS job_executions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL UNIQUE,
			server_id INTEGER NOT NULL,
			server_name TEXT,
			type TEXT NOT NULL,
			tool TEXT,
			job_name TEXT,
			device_ids TEXT,
			state TEXT NOT NULL DEFAULT 'running',
			config TEXT,
			result_summary TEXT,
			scheduled_job_id INTEGER,
			retry_attempt INTEGER NOT NULL DEFAULT 0,
			error_message TEXT,
			started_at TEXT,
			completed_at TEXT,
			created_at TEXT NOT NULL,
			trace_raw_key TEXT,
			trace_raw_format TEXT,
			trace_raw_size INTEGER,
			trace_raw_uploaded_at TEXT,
			trace_parquet_keys TEXT,
			trace_parsed_at TEXT,
			trace_parse_state TEXT,
			trace_parse_error TEXT,
			workload_note TEXT,
			trace_jobs TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_job_executions_server_id ON job_executions(server_id)`,
		`CREATE INDEX IF NOT EXISTS idx_job_executions_state ON job_executions(state)`,
		`CREATE INDEX IF NOT EXISTS idx_job_executions_created_at ON job_executions(created_at DESC)`,

		// BenchmarkPreset — portal portal_benchmark_presets
		`CREATE TABLE IF NOT EXISTS benchmark_presets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			description TEXT,
			tool TEXT NOT NULL,
			params_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,

		// IOTestPreset — portal portal_iotest_presets
		`CREATE TABLE IF NOT EXISTS iotest_presets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			description TEXT,
			category TEXT NOT NULL,
			config_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,

		// ScenarioTemplate — portal portal_scenario_templates
		`CREATE TABLE IF NOT EXISTS scenario_templates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			description TEXT,
			repeat_count INTEGER NOT NULL DEFAULT 1,
			steps_json TEXT NOT NULL,
			loops_json TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,

		// AppMacro — portal portal_app_macros
		`CREATE TABLE IF NOT EXISTS app_macros (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			description TEXT,
			package_name TEXT,
			events_json TEXT NOT NULL,
			device_width INTEGER,
			device_height INTEGER,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,

		// ScheduledJob — portal portal_scheduled_jobs
		`CREATE TABLE IF NOT EXISTS scheduled_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			description TEXT,
			enabled INTEGER NOT NULL DEFAULT 1,
			type TEXT NOT NULL,
			server_id INTEGER NOT NULL,
			device_ids TEXT NOT NULL,
			config TEXT NOT NULL,
			cron_expression TEXT NOT NULL,
			busy_policy TEXT DEFAULT 'reject',
			retry_count INTEGER NOT NULL DEFAULT 0,
			retry_delay_seconds INTEGER NOT NULL DEFAULT 60,
			notify_on_failure INTEGER NOT NULL DEFAULT 0,
			notify_on_success INTEGER NOT NULL DEFAULT 0,
			notify_webhook_url TEXT,
			last_run_at TEXT,
			last_run_status TEXT,
			next_run_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w (stmt: %s)", err, firstLine(s))
		}
	}

	// 기존 DB 에 신규 컬럼을 추가 (CREATE TABLE IF NOT EXISTS 는 스키마 변경을 반영 못함).
	// 이미 컬럼이 있으면 "duplicate column" 에러가 나므로 무시한다.
	addColumns := []string{
		// job 상세 "무엇이 돌았고 왜 이렇게 동작했나" 워크로드 컨텍스트 — 사용자 메모 오버라이드
		`ALTER TABLE job_executions ADD COLUMN workload_note TEXT`,
		// trace job 매핑(JSON) 영속화 — 만료된 job 도 job 상세에서 기존 trace UI 로 진입 가능
		`ALTER TABLE job_executions ADD COLUMN trace_jobs TEXT`,
	}
	for _, s := range addColumns {
		if _, err := db.Exec(s); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("migrate(add column): %w (stmt: %s)", err, firstLine(s))
		}
	}
	return nil
}

// SeedLocalServer — standalone 부팅 시 'localhost' agent 자기 자신을 자동 INSERT.
// 이름은 "localhost (this agent)". 이미 동일 host:port 가 있으면 INSERT 생략.
func (db *DB) SeedLocalServer(host string, port int) (int64, error) {
	const q = `SELECT id FROM agent_servers WHERE host=? AND port=? LIMIT 1`
	var id int64
	err := db.QueryRow(q, host, port).Scan(&id)
	if err == nil {
		return id, nil // 이미 존재
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("seed local server lookup: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const ins = `INSERT INTO agent_servers
		(name, host, port, enabled, description, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?, ?)`
	name := fmt.Sprintf("localhost (this agent:%d)", port)
	res, err := db.Exec(ins, name, host, port, "Auto-registered local standalone agent", now, now)
	if err != nil {
		return 0, fmt.Errorf("seed local server insert: %w", err)
	}
	return res.LastInsertId()
}

// DefaultPath — config 에서 미지정 시 사용. $HOME/.agent-standalone/agent.db
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "agent-standalone.db"
	}
	return filepath.Join(home, ".agent-standalone", "agent.db")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
