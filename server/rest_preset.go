package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent/storage/sqlitedb"
)

// registerPresetRoutes — portal AgentController 의 benchmark-presets / iotest-presets / scenario-templates.
//
//	GET    /api/agent/benchmark-presets
//	POST   /api/agent/benchmark-presets
//	PUT    /api/agent/benchmark-presets/{id}
//	DELETE /api/agent/benchmark-presets/{id}
//	GET    /api/agent/iotest-presets
//	POST   /api/agent/iotest-presets
//	PUT    /api/agent/iotest-presets/{id}
//	DELETE /api/agent/iotest-presets/{id}
//	GET    /api/agent/scenario-templates
//	POST   /api/agent/scenario-templates
//	PUT    /api/agent/scenario-templates/{id}
//	DELETE /api/agent/scenario-templates/{id}
//	POST   /api/agent/scenario-templates/{id}/duplicate
func registerPresetRoutes(mux *http.ServeMux, db *sqlitedb.DB) {
	// ---------- BenchmarkPreset ----------
	mux.HandleFunc("/api/agent/benchmark-presets", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			list, err := db.ListBenchmarkPresets(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			out := make([]map[string]any, 0, len(list))
			for _, p := range list {
				out = append(out, benchmarkPresetToMap(p))
			}
			writeJSON(w, http.StatusOK, out)
		case http.MethodPost:
			body, err := readJSONBody(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, "decode: "+err.Error())
				return
			}
			created, err := db.CreateBenchmarkPreset(r.Context(), buildBenchmarkPresetFromBody(body))
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, benchmarkPresetToMap(created))
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/api/agent/benchmark-presets/", func(w http.ResponseWriter, r *http.Request) {
		idStr := strings.TrimPrefix(r.URL.Path, "/api/agent/benchmark-presets/")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id: "+idStr)
			return
		}
		switch r.Method {
		case http.MethodPut:
			body, err := readJSONBody(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, "decode: "+err.Error())
				return
			}
			updated, err := db.UpdateBenchmarkPreset(r.Context(), id, buildBenchmarkPresetFromBody(body))
			if errors.Is(err, sqlitedb.ErrNotFound) {
				writeError(w, http.StatusNotFound, "preset not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, benchmarkPresetToMap(updated))
		case http.MethodDelete:
			if err := db.DeleteBenchmarkPreset(r.Context(), id); err != nil {
				if errors.Is(err, sqlitedb.ErrNotFound) {
					writeError(w, http.StatusNotFound, "preset not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"success": true})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// ---------- IOTestPreset ----------
	mux.HandleFunc("/api/agent/iotest-presets", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			list, err := db.ListIOTestPresets(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			out := make([]map[string]any, 0, len(list))
			for _, p := range list {
				out = append(out, iotestPresetToMap(p))
			}
			writeJSON(w, http.StatusOK, out)
		case http.MethodPost:
			body, err := readJSONBody(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, "decode: "+err.Error())
				return
			}
			created, err := db.CreateIOTestPreset(r.Context(), buildIOTestPresetFromBody(body))
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, iotestPresetToMap(created))
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/api/agent/iotest-presets/", func(w http.ResponseWriter, r *http.Request) {
		idStr := strings.TrimPrefix(r.URL.Path, "/api/agent/iotest-presets/")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id: "+idStr)
			return
		}
		switch r.Method {
		case http.MethodPut:
			body, err := readJSONBody(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, "decode: "+err.Error())
				return
			}
			updated, err := db.UpdateIOTestPreset(r.Context(), id, buildIOTestPresetFromBody(body))
			if errors.Is(err, sqlitedb.ErrNotFound) {
				writeError(w, http.StatusNotFound, "preset not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, iotestPresetToMap(updated))
		case http.MethodDelete:
			if err := db.DeleteIOTestPreset(r.Context(), id); err != nil {
				if errors.Is(err, sqlitedb.ErrNotFound) {
					writeError(w, http.StatusNotFound, "preset not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"success": true})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// ---------- ScenarioTemplate ----------
	mux.HandleFunc("/api/agent/scenario-templates", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			list, err := db.ListScenarioTemplates(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			out := make([]map[string]any, 0, len(list))
			for _, t := range list {
				out = append(out, scenarioTemplateToMap(t))
			}
			writeJSON(w, http.StatusOK, out)
		case http.MethodPost:
			body, err := readJSONBody(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, "decode: "+err.Error())
				return
			}
			created, err := db.CreateScenarioTemplate(r.Context(), buildScenarioTemplateFromBody(body))
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, scenarioTemplateToMap(created))
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/api/agent/scenario-templates/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/agent/scenario-templates/")
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

		// duplicate
		if len(parts) == 2 && parts[1] == "duplicate" && r.Method == http.MethodPost {
			src, err := db.FindScenarioTemplate(r.Context(), id)
			if errors.Is(err, sqlitedb.ErrNotFound) {
				writeError(w, http.StatusNotFound, "template not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			clone := *src
			clone.ID = 0
			clone.Name = src.Name + " (copy)"
			created, err := db.CreateScenarioTemplate(r.Context(), &clone)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, scenarioTemplateToMap(created))
			return
		}

		// /scenario-templates/{id}
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodPut:
				body, err := readJSONBody(r)
				if err != nil {
					writeError(w, http.StatusBadRequest, "decode: "+err.Error())
					return
				}
				updated, err := db.UpdateScenarioTemplate(r.Context(), id, buildScenarioTemplateFromBody(body))
				if errors.Is(err, sqlitedb.ErrNotFound) {
					writeError(w, http.StatusNotFound, "template not found")
					return
				}
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, scenarioTemplateToMap(updated))
			case http.MethodDelete:
				if err := db.DeleteScenarioTemplate(r.Context(), id); err != nil {
					if errors.Is(err, sqlitedb.ErrNotFound) {
						writeError(w, http.StatusNotFound, "template not found")
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

// ---------- preset/template builders & mappers ----------

func buildBenchmarkPresetFromBody(body map[string]any) *sqlitedb.BenchmarkPreset {
	p := &sqlitedb.BenchmarkPreset{}
	if v, ok := body["name"].(string); ok {
		p.Name = v
	}
	if v, ok := body["description"].(string); ok {
		p.Description = v
	}
	if v, ok := body["tool"].(string); ok {
		p.Tool = v
	}
	if v, ok := body["paramsJson"].(string); ok {
		p.ParamsJSON = v
	} else if v, exists := body["paramsJson"]; exists && v != nil {
		if data, err := json.Marshal(v); err == nil {
			p.ParamsJSON = string(data)
		}
	}
	return p
}

func benchmarkPresetToMap(p *sqlitedb.BenchmarkPreset) map[string]any {
	return map[string]any{
		"id":          p.ID,
		"name":        p.Name,
		"description": p.Description,
		"tool":        p.Tool,
		"paramsJson":  p.ParamsJSON,
		"createdAt":   p.CreatedAt.Format(time.RFC3339Nano),
		"updatedAt":   p.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func buildIOTestPresetFromBody(body map[string]any) *sqlitedb.IOTestPreset {
	p := &sqlitedb.IOTestPreset{}
	if v, ok := body["name"].(string); ok {
		p.Name = v
	}
	if v, ok := body["description"].(string); ok {
		p.Description = v
	}
	if v, ok := body["category"].(string); ok {
		p.Category = v
	}
	if v, ok := body["configJson"].(string); ok {
		p.ConfigJSON = v
	} else if v, exists := body["configJson"]; exists && v != nil {
		if data, err := json.Marshal(v); err == nil {
			p.ConfigJSON = string(data)
		}
	}
	return p
}

func iotestPresetToMap(p *sqlitedb.IOTestPreset) map[string]any {
	return map[string]any{
		"id":          p.ID,
		"name":        p.Name,
		"description": p.Description,
		"category":    p.Category,
		"configJson":  p.ConfigJSON,
		"createdAt":   p.CreatedAt.Format(time.RFC3339Nano),
		"updatedAt":   p.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func buildScenarioTemplateFromBody(body map[string]any) *sqlitedb.ScenarioTemplate {
	t := &sqlitedb.ScenarioTemplate{RepeatCount: 1}
	if v, ok := body["name"].(string); ok {
		t.Name = v
	}
	if v, ok := body["description"].(string); ok {
		t.Description = v
	}
	if v, ok := numberOf(body["repeatCount"]); ok && v > 0 {
		t.RepeatCount = int(v)
	}
	if v, ok := body["stepsJson"].(string); ok {
		t.StepsJSON = v
	} else if v, exists := body["stepsJson"]; exists && v != nil {
		if data, err := json.Marshal(v); err == nil {
			t.StepsJSON = string(data)
		}
	}
	// loopsJson 은 nullable
	if v, ok := body["loopsJson"].(string); ok && v != "" {
		t.LoopsJSON = nullStr(v)
	} else if v, exists := body["loopsJson"]; exists && v != nil {
		if data, err := json.Marshal(v); err == nil {
			t.LoopsJSON = nullStr(string(data))
		}
	}
	return t
}

func scenarioTemplateToMap(t *sqlitedb.ScenarioTemplate) map[string]any {
	return map[string]any{
		"id":          t.ID,
		"name":        t.Name,
		"description": t.Description,
		"repeatCount": t.RepeatCount,
		"stepsJson":   t.StepsJSON,
		"loopsJson":   nullString(t.LoopsJSON),
		"createdAt":   t.CreatedAt.Format(time.RFC3339Nano),
		"updatedAt":   t.UpdatedAt.Format(time.RFC3339Nano),
	}
}
