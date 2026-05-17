package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent/schedule"
	"agent/storage/sqlitedb"
)

// registerScheduleRoutes — portal ScheduledJobController (/api/agent/schedules/*).
// standalone 에는 권한 체크 없음 (인증 비활성화).
//
//	GET    /api/agent/schedules
//	GET    /api/agent/schedules/{id}
//	POST   /api/agent/schedules
//	PUT    /api/agent/schedules/{id}
//	DELETE /api/agent/schedules/{id}
//	POST   /api/agent/schedules/{id}/trigger
//	POST   /api/agent/schedules/{id}/enable
func registerScheduleRoutes(mux *http.ServeMux, db *sqlitedb.DB, runner *schedule.Runner) {
	mux.HandleFunc("/api/agent/schedules", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			list, err := db.ListScheduledJobs(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			out := make([]map[string]any, 0, len(list))
			for _, s := range list {
				out = append(out, scheduledJobToMap(s))
			}
			writeJSON(w, http.StatusOK, out)
		case http.MethodPost:
			body, err := readJSONBody(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, "decode: "+err.Error())
				return
			}
			created, err := db.CreateScheduledJob(r.Context(), buildScheduledJobFromBody(body))
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if runner != nil {
				runner.Reload(r.Context())
			}
			writeJSON(w, http.StatusOK, scheduledJobToMap(created))
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/api/agent/schedules/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/agent/schedules/")
		parts := strings.Split(rest, "/")
		if len(parts) == 0 || parts[0] == "" {
			writeError(w, http.StatusNotFound, "id required")
			return
		}
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id: "+parts[0])
			return
		}

		// /{id}/trigger
		if len(parts) == 2 && parts[1] == "trigger" {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if runner == nil {
				writeError(w, http.StatusServiceUnavailable, "scheduler not running")
				return
			}
			jobID, err := runner.Trigger(r.Context(), id)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "jobId": jobID})
			return
		}

		// /{id}/enable
		if len(parts) == 2 && parts[1] == "enable" {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			cur, err := db.FindScheduledJob(r.Context(), id)
			if errors.Is(err, sqlitedb.ErrNotFound) {
				writeError(w, http.StatusNotFound, "schedule not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			toggled, err := db.ToggleScheduledJobEnabled(r.Context(), id, !cur.Enabled)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if runner != nil {
				runner.Reload(r.Context())
			}
			writeJSON(w, http.StatusOK, scheduledJobToMap(toggled))
			return
		}

		// /{id}
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				s, err := db.FindScheduledJob(r.Context(), id)
				if errors.Is(err, sqlitedb.ErrNotFound) {
					writeError(w, http.StatusNotFound, "schedule not found")
					return
				}
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, scheduledJobToMap(s))
			case http.MethodPut:
				body, err := readJSONBody(r)
				if err != nil {
					writeError(w, http.StatusBadRequest, "decode: "+err.Error())
					return
				}
				updated, err := db.UpdateScheduledJob(r.Context(), id, buildScheduledJobFromBody(body))
				if errors.Is(err, sqlitedb.ErrNotFound) {
					writeError(w, http.StatusNotFound, "schedule not found")
					return
				}
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				if runner != nil {
					runner.Reload(r.Context())
				}
				writeJSON(w, http.StatusOK, scheduledJobToMap(updated))
			case http.MethodDelete:
				if err := db.DeleteScheduledJob(r.Context(), id); err != nil {
					if errors.Is(err, sqlitedb.ErrNotFound) {
						writeError(w, http.StatusNotFound, "schedule not found")
						return
					}
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				if runner != nil {
					runner.Reload(r.Context())
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

// ---------- builders / mappers ----------

func buildScheduledJobFromBody(body map[string]any) *sqlitedb.ScheduledJob {
	s := &sqlitedb.ScheduledJob{
		Enabled:           true,
		BusyPolicy:        "reject",
		RetryDelaySeconds: 60,
	}
	if v, ok := body["name"].(string); ok {
		s.Name = v
	}
	if v, ok := body["description"].(string); ok {
		s.Description = v
	}
	if v, ok := body["enabled"].(bool); ok {
		s.Enabled = v
	}
	if v, ok := body["type"].(string); ok {
		s.Type = v
	}
	if v, ok := numberOf(body["serverId"]); ok {
		s.ServerID = int64(v)
	}
	if v, ok := body["deviceIds"].(string); ok {
		s.DeviceIDs = v
	}
	if v, ok := body["config"].(string); ok {
		s.Config = v
	}
	if v, ok := body["cronExpression"].(string); ok {
		s.CronExpression = v
	}
	if v, ok := body["busyPolicy"].(string); ok && v != "" {
		s.BusyPolicy = v
	}
	if v, ok := numberOf(body["retryCount"]); ok {
		s.RetryCount = int(v)
	}
	if v, ok := numberOf(body["retryDelaySeconds"]); ok && v > 0 {
		s.RetryDelaySeconds = int(v)
	}
	if v, ok := body["notifyOnFailure"].(bool); ok {
		s.NotifyOnFailure = v
	}
	if v, ok := body["notifyOnSuccess"].(bool); ok {
		s.NotifyOnSuccess = v
	}
	if v, ok := body["notifyWebhookUrl"].(string); ok {
		s.NotifyWebhookURL = nullStr(v)
	}
	return s
}

func scheduledJobToMap(s *sqlitedb.ScheduledJob) map[string]any {
	return map[string]any{
		"id":                s.ID,
		"name":              s.Name,
		"description":       s.Description,
		"enabled":           s.Enabled,
		"type":              s.Type,
		"serverId":          s.ServerID,
		"deviceIds":         s.DeviceIDs,
		"config":            s.Config,
		"cronExpression":    s.CronExpression,
		"busyPolicy":        s.BusyPolicy,
		"retryCount":        s.RetryCount,
		"retryDelaySeconds": s.RetryDelaySeconds,
		"notifyOnFailure":   s.NotifyOnFailure,
		"notifyOnSuccess":   s.NotifyOnSuccess,
		"notifyWebhookUrl":  nullString(s.NotifyWebhookURL),
		"lastRunAt":         nullTime(s.LastRunAt),
		"lastRunStatus":     nullString(s.LastRunStatus),
		"nextRunAt":         nullTime(s.NextRunAt),
		"createdAt":         s.CreatedAt.Format(time.RFC3339Nano),
		"updatedAt":         s.UpdatedAt.Format(time.RFC3339Nano),
	}
}
