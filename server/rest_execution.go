package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent/storage/sqlitedb"
)

// registerExecutionRoutes — portal JobExecutionController (/api/agent/executions/*) 와 동일.
//
//	GET    /api/agent/executions?serverId=&type=&state=&from=&to=&page=&size=
//	GET    /api/agent/executions/{id}
//	GET    /api/agent/executions/by-job-id/{jobId}
//	DELETE /api/agent/executions/{id}
//	GET    /api/agent/executions/stats?serverId=
func registerExecutionRoutes(mux *http.ServeMux, db *sqlitedb.DB) {
	mux.HandleFunc("/api/agent/executions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		q := r.URL.Query()
		filter := sqlitedb.JobExecutionFilter{
			Type:  q.Get("type"),
			State: q.Get("state"),
		}
		if sidStr := q.Get("serverId"); sidStr != "" {
			if id, err := strconv.ParseInt(sidStr, 10, 64); err == nil {
				filter.ServerID = &id
			}
		}
		page := 0
		size := 30
		if v := q.Get("page"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				page = n
			}
		}
		if v := q.Get("size"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
				size = n
			}
		}
		filter.Limit = size
		filter.Offset = page * size

		list, total, err := db.ListJobExecutions(r.Context(), filter)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		content := make([]map[string]any, 0, len(list))
		for _, e := range list {
			content = append(content, executionToMap(e))
		}
		// Spring Page<T> 응답 형식
		totalPages := (total + size - 1) / size
		writeJSON(w, http.StatusOK, map[string]any{
			"content":       content,
			"totalElements": total,
			"totalPages":    totalPages,
			"page":          page,
			"size":          size,
		})
	})

	mux.HandleFunc("/api/agent/executions/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/agent/executions/")
		parts := strings.Split(rest, "/")

		// GET /api/agent/executions/stats
		if len(parts) == 1 && parts[0] == "stats" {
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			var serverID *int64
			if sidStr := r.URL.Query().Get("serverId"); sidStr != "" {
				if id, err := strconv.ParseInt(sidStr, 10, 64); err == nil {
					serverID = &id
				}
			}
			stats, err := db.GetExecutionStats(r.Context(), serverID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, stats)
			return
		}

		// PUT /api/agent/executions/by-job-id/{jobId}/workload-note
		// body: {"note": "..."} — 빈 문자열이면 규칙 자동 해석으로 되돌림.
		if len(parts) == 3 && parts[0] == "by-job-id" && parts[2] == "workload-note" {
			if r.Method != http.MethodPut {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			var body struct {
				Note string `json:"note"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
				return
			}
			if err := db.UpdateJobExecutionWorkloadNote(r.Context(), parts[1], body.Note); err != nil {
				if errors.Is(err, sqlitedb.ErrNotFound) {
					writeError(w, http.StatusNotFound, "execution not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "workloadNote": body.Note})
			return
		}

		// GET /api/agent/executions/by-job-id/{jobId}
		if len(parts) == 2 && parts[0] == "by-job-id" {
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			e, err := db.FindJobExecutionByJobID(r.Context(), parts[1])
			if errors.Is(err, sqlitedb.ErrNotFound) {
				writeError(w, http.StatusNotFound, "execution not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, executionToMap(e))
			return
		}

		// GET/DELETE /api/agent/executions/{id}
		if len(parts) == 1 {
			id, err := strconv.ParseInt(parts[0], 10, 64)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid id: "+parts[0])
				return
			}
			switch r.Method {
			case http.MethodGet:
				e, err := db.FindJobExecution(r.Context(), id)
				if errors.Is(err, sqlitedb.ErrNotFound) {
					writeError(w, http.StatusNotFound, "execution not found")
					return
				}
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, executionToMap(e))
			case http.MethodDelete:
				if err := db.DeleteJobExecution(r.Context(), id); err != nil {
					if errors.Is(err, sqlitedb.ErrNotFound) {
						writeError(w, http.StatusNotFound, "execution not found")
						return
					}
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"success": true})
			default:
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}

		writeError(w, http.StatusNotFound, "not found")
	})
}

// executionToMap — portal JobExecutionController.toMap 와 동일한 키/순서.
func executionToMap(e *sqlitedb.JobExecution) map[string]any {
	return map[string]any{
		"id":                  e.ID,
		"jobId":               e.JobID,
		"serverId":            e.ServerID,
		"serverName":          nullString(e.ServerName),
		"type":                e.Type,
		"tool":                nullString(e.Tool),
		"jobName":             nullString(e.JobName),
		"deviceIds":           nullString(e.DeviceIDs),
		"state":               e.State,
		"config":              nullString(e.Config),
		"resultSummary":       nullString(e.ResultSummary),
		"scheduledJobId":      nullInt64(e.ScheduledJobID),
		"retryAttempt":        e.RetryAttempt,
		"errorMessage":        nullString(e.ErrorMessage),
		"startedAt":           nullTime(e.StartedAt),
		"completedAt":         nullTime(e.CompletedAt),
		"createdAt":           e.CreatedAt.Format(time.RFC3339Nano),
		"traceRawKey":         nullString(e.TraceRawKey),
		"traceRawFormat":      nullString(e.TraceRawFormat),
		"traceRawSize":        nullInt64(e.TraceRawSize),
		"traceRawUploadedAt":  nullTime(e.TraceRawUploadedAt),
		"traceParquetKeys":    nullString(e.TraceParquetKeys),
		"traceParsedAt":       nullTime(e.TraceParsedAt),
		"traceParseState":     nullString(e.TraceParseState),
		"traceParseError":     nullString(e.TraceParseError),
		"workloadNote":        nullString(e.WorkloadNote),
		"traceJobs":           nullJSONArray(e.TraceJobs),
	}
}

// nullJSONArray — trace_jobs 처럼 JSON array 문자열로 저장된 컬럼을 파싱된 배열로 반환.
// 파싱 실패/미설정이면 nil (FE 에서 없는 것으로 처리).
func nullJSONArray(s sql.NullString) any {
	if !s.Valid || s.String == "" {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(s.String), &arr); err != nil {
		return nil
	}
	return arr
}

func nullString(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

func nullInt64(n sql.NullInt64) any {
	if !n.Valid {
		return nil
	}
	return n.Int64
}

func nullTime(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time.Format(time.RFC3339Nano)
}
