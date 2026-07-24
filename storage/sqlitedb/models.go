package sqlitedb

import (
	"database/sql"
	"time"
)

// 모든 모델은 portal/entity/*.java 의 필드명을 camelCase 그대로 따라간다.
// JSON 응답 시 portal 와 동일 키가 나가도록 한다.

// AgentServer — host:port 등록된 agent 서버.
// standalone 모드에선 부팅 시 localhost 자기 자신이 자동 INSERT 된다.
type AgentServer struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Enabled     bool      `json:"enabled"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// JobExecution — benchmark/scenario/trace 실행 이력.
// 잡 진행률 SSE / Subscribe 콜백에서 상태 업데이트.
type JobExecution struct {
	ID             int64          `json:"id"`
	JobID          string         `json:"jobId"`
	ServerID       int64          `json:"serverId"`
	ServerName     sql.NullString `json:"serverName"`
	Type           string         `json:"type"` // benchmark / scenario / trace
	Tool           sql.NullString `json:"tool"`
	JobName        sql.NullString `json:"jobName"`
	DeviceIDs      sql.NullString `json:"deviceIds"` // JSON array string
	State          string         `json:"state"`
	Config         sql.NullString `json:"config"` // JSON
	ResultSummary  sql.NullString `json:"resultSummary"`
	ScheduledJobID sql.NullInt64  `json:"scheduledJobId"`
	RetryAttempt   int            `json:"retryAttempt"`
	ErrorMessage   sql.NullString `json:"errorMessage"`
	StartedAt      sql.NullTime   `json:"startedAt"`
	CompletedAt    sql.NullTime   `json:"completedAt"`
	CreatedAt      time.Time      `json:"createdAt"`

	// WorkloadNote — job 상세 워크로드 컨텍스트에서 사용자가 직접 남긴 메모 (규칙 자동 해석 오버라이드).
	WorkloadNote sql.NullString `json:"workloadNote"`

	// TraceJobs — 이 job 에 연결된 trace job 매핑 JSON array (step/loop/repeat/type + traceJobId).
	// 만료된 job 도 job 상세에서 기존 trace UI 로 진입할 수 있게 영속화.
	TraceJobs sql.NullString `json:"traceJobs"`

	// Trace archive 메타 (nullable)
	TraceRawKey         sql.NullString `json:"traceRawKey"`
	TraceRawFormat      sql.NullString `json:"traceRawFormat"`
	TraceRawSize        sql.NullInt64  `json:"traceRawSize"`
	TraceRawUploadedAt  sql.NullTime   `json:"traceRawUploadedAt"`
	TraceParquetKeys    sql.NullString `json:"traceParquetKeys"`
	TraceParsedAt       sql.NullTime   `json:"traceParsedAt"`
	TraceParseState     sql.NullString `json:"traceParseState"`
	TraceParseError     sql.NullString `json:"traceParseError"`
}

// BenchmarkPreset — FIO/IOZONE/TIOTEST/IOTEST 의 params 프리셋.
type BenchmarkPreset struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Tool        string    `json:"tool"`
	ParamsJSON  string    `json:"paramsJson"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// IOTestPreset — 별도 카테고리 분류, configJson 구조 다름.
type IOTestPreset struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	ConfigJSON  string    `json:"configJson"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ScenarioTemplate — multi-step 시나리오 정의 (steps + loops).
type ScenarioTemplate struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	RepeatCount int            `json:"repeatCount"`
	StepsJSON   string         `json:"stepsJson"`
	LoopsJSON   sql.NullString `json:"loopsJson"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

// AppMacro — macro recorder 결과 (events + 디바이스 해상도).
type AppMacro struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	PackageName  sql.NullString `json:"packageName"`
	EventsJSON   string         `json:"eventsJson"`
	DeviceWidth  sql.NullInt32  `json:"deviceWidth"`
	DeviceHeight sql.NullInt32  `json:"deviceHeight"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

// ScheduledJob — cron 표현식 기반 자동 실행 잡.
type ScheduledJob struct {
	ID                int64          `json:"id"`
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	Enabled           bool           `json:"enabled"`
	Type              string         `json:"type"`
	ServerID          int64          `json:"serverId"`
	DeviceIDs         string         `json:"deviceIds"`
	Config            string         `json:"config"`
	CronExpression    string         `json:"cronExpression"`
	BusyPolicy        string         `json:"busyPolicy"`
	RetryCount        int            `json:"retryCount"`
	RetryDelaySeconds int            `json:"retryDelaySeconds"`
	NotifyOnFailure   bool           `json:"notifyOnFailure"`
	NotifyOnSuccess   bool           `json:"notifyOnSuccess"`
	NotifyWebhookURL  sql.NullString `json:"notifyWebhookUrl"`
	LastRunAt         sql.NullTime   `json:"lastRunAt"`
	LastRunStatus     sql.NullString `json:"lastRunStatus"`
	NextRunAt         sql.NullTime   `json:"nextRunAt"`
	CreatedAt         time.Time      `json:"createdAt"`
	UpdatedAt         time.Time      `json:"updatedAt"`
}
