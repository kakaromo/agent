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

// AI log profile CRUD — on-device AI(LLM) 평가용 logcat 패턴 프리셋.
//
// 구조는 rest_preset.go 의 BenchmarkPreset 핸들러 쌍을 그대로 복제한다
// (컬렉션 경로 + 트레일링 슬래시 아이템 경로).
//
//	GET    /api/agent/ai-log-profiles          — 목록 (runtime/soc 로 필터 가능)
//	POST   /api/agent/ai-log-profiles          — 생성
//	GET    /api/agent/ai-log-profiles/{id}     — 단건
//	PUT    /api/agent/ai-log-profiles/{id}     — 수정
//	DELETE /api/agent/ai-log-profiles/{id}     — 삭제
func registerAILogProfileRoutes(mux *http.ServeMux, db *sqlitedb.DB) {
	mux.HandleFunc("/api/agent/ai-log-profiles", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			list, err := db.ListAILogProfiles(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			// runtime/soc 필터 — 기기가 붙었을 때 "이 AP 에 맞는 프로파일" 을
			// 골라주기 위한 것이다. 컬럼으로 뺀 이유가 이거다.
			//
			// ⚠ soc 필터는 **빈 soc 프로파일도 포함**한다. 빈 값은 "런타임 공용"
			// 이라는 뜻이라 특정 soc 를 물었을 때 배제하면 안 된다.
			runtime := strings.TrimSpace(r.URL.Query().Get("runtime"))
			soc := strings.TrimSpace(r.URL.Query().Get("soc"))
			out := make([]map[string]any, 0, len(list))
			for _, p := range list {
				if runtime != "" && !strings.EqualFold(p.Runtime, runtime) {
					continue
				}
				if soc != "" && p.SOC != "" && !strings.EqualFold(p.SOC, soc) {
					continue
				}
				out = append(out, aiLogProfileToMap(p))
			}
			writeJSON(w, http.StatusOK, out)
		case http.MethodPost:
			body, err := readJSONBody(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, "decode: "+err.Error())
				return
			}
			created, err := db.CreateAILogProfile(r.Context(), buildAILogProfileFromBody(body))
			if err != nil {
				// ⚠ 검증 실패(잘못된 정규식·캡처 그룹 없음·키 중복)는 사용자 입력
				// 문제라 400 이어야 한다. 500 을 주면 서버 탓처럼 보여서 사용자가
				// 자기 패턴을 고칠 생각을 못 한다.
				writeError(w, statusForProfileErr(err), err.Error())
				return
			}
			writeJSON(w, http.StatusOK, aiLogProfileToMap(created))
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/api/agent/ai-log-profiles/", func(w http.ResponseWriter, r *http.Request) {
		idStr := strings.TrimPrefix(r.URL.Path, "/api/agent/ai-log-profiles/")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id: "+idStr)
			return
		}
		switch r.Method {
		case http.MethodGet:
			p, err := db.FindAILogProfile(r.Context(), id)
			if errors.Is(err, sqlitedb.ErrNotFound) {
				writeError(w, http.StatusNotFound, "profile not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, aiLogProfileToMap(p))
		case http.MethodPut:
			body, err := readJSONBody(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, "decode: "+err.Error())
				return
			}
			updated, err := db.UpdateAILogProfile(r.Context(), id, buildAILogProfileFromBody(body))
			if errors.Is(err, sqlitedb.ErrNotFound) {
				writeError(w, http.StatusNotFound, "profile not found")
				return
			}
			if err != nil {
				writeError(w, statusForProfileErr(err), err.Error())
				return
			}
			writeJSON(w, http.StatusOK, aiLogProfileToMap(updated))
		case http.MethodDelete:
			if err := db.DeleteAILogProfile(r.Context(), id); err != nil {
				if errors.Is(err, sqlitedb.ErrNotFound) {
					writeError(w, http.StatusNotFound, "profile not found")
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
}

// statusForProfileErr — 사용자 입력 문제와 서버 문제를 가른다.
//
// 검증 메시지는 전부 "무엇을 어떻게 고쳐야 하는지" 를 담고 있어서 그대로
// 400 으로 돌려주는 것이 사용자에게 가장 도움이 된다.
func statusForProfileErr(err error) int {
	msg := err.Error()
	for _, s := range []string{"required", "regex", "캡처 그룹", "중복", "patternsJson"} {
		if strings.Contains(msg, s) {
			return http.StatusBadRequest
		}
	}
	return http.StatusInternalServerError
}

func buildAILogProfileFromBody(body map[string]any) *sqlitedb.AILogProfile {
	p := &sqlitedb.AILogProfile{}
	if v, ok := body["name"].(string); ok {
		p.Name = v
	}
	if v, ok := body["description"].(string); ok {
		p.Description = v
	}
	if v, ok := body["runtime"].(string); ok {
		p.Runtime = v
	}
	if v, ok := body["soc"].(string); ok {
		p.SOC = v
	}
	// JSON 필드는 문자열/객체 양쪽을 받는다 (기존 프리셋과 같은 dual-form 처리).
	// UI 가 객체로 보내는 편이 자연스럽고, 스크립트는 문자열이 편하다.
	if v, ok := body["patternsJson"].(string); ok {
		p.PatternsJSON = v
	} else if v, exists := body["patternsJson"]; exists && v != nil {
		if data, err := json.Marshal(v); err == nil {
			p.PatternsJSON = string(data)
		}
	}
	return p
}

func aiLogProfileToMap(p *sqlitedb.AILogProfile) map[string]any {
	return map[string]any{
		"id":           p.ID,
		"name":         p.Name,
		"description":  p.Description,
		"runtime":      p.Runtime,
		"soc":          p.SOC,
		"patternsJson": p.PatternsJSON,
		"createdAt":    p.CreatedAt.Format(time.RFC3339Nano),
		"updatedAt":    p.UpdatedAt.Format(time.RFC3339Nano),
	}
}
