package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"agent/storage/sqlitedb"
)

// 시나리오 이식 포맷 (docs/schemas/scenario.schema.json / docs/adr/0001-scenario-portability.md).
//
// DB 는 그대로 두고 자체완결 JSON 파일로 export/import 만 얹는다. steps 는 proto shape 을
// 가공 없이 담고, requirements 메타데이터(설치앱·해상도·trace 타입)를 덧씌워 다른 환경 이식 시
// "조용히 깨지는" 것을 막는다.

// scenarioExportSchemaVersion — 이식 포맷 버전. 스텝 구조 변경 시 올린다.
const scenarioExportSchemaVersion = 1

// scenarioExport — export/import 파일의 최상위 구조.
type scenarioExport struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Kind          string                `json:"kind"` // "scenario"
	Name          string                `json:"name"`
	Description   string                `json:"description,omitempty"`
	RepeatCount   int                   `json:"repeatCount,omitempty"`
	Steps         []any                 `json:"steps"`
	Loops         []any                 `json:"loops,omitempty"`
	Requirements  *scenarioRequirements `json:"requirements,omitempty"`
	Origin        map[string]any        `json:"origin,omitempty"`
}

type scenarioRequirements struct {
	Packages     []string `json:"packages,omitempty"`
	SourceWidth  int      `json:"sourceWidth,omitempty"`
	SourceHeight int      `json:"sourceHeight,omitempty"`
	TraceType    string   `json:"traceType,omitempty"`
}

// buildScenarioExport — DB 템플릿 → 이식 포맷. requirements 는 steps 에서 자동 수집한다.
func buildScenarioExport(t *sqlitedb.ScenarioTemplate) (*scenarioExport, error) {
	var steps []any
	if t.StepsJSON != "" {
		if err := json.Unmarshal([]byte(t.StepsJSON), &steps); err != nil {
			return nil, fmt.Errorf("parse stepsJson: %w", err)
		}
	}
	var loops []any
	if t.LoopsJSON.Valid && t.LoopsJSON.String != "" {
		if err := json.Unmarshal([]byte(t.LoopsJSON.String), &loops); err != nil {
			return nil, fmt.Errorf("parse loopsJson: %w", err)
		}
	}

	exp := &scenarioExport{
		SchemaVersion: scenarioExportSchemaVersion,
		Kind:          "scenario",
		Name:          t.Name,
		Description:   t.Description,
		RepeatCount:   t.RepeatCount,
		Steps:         steps,
		Loops:         loops,
		Requirements:  collectScenarioRequirements(steps),
	}
	exp.Origin = map[string]any{
		"contentHash": scenarioContentHash(t.StepsJSON, nullString(t.LoopsJSON)),
	}
	return exp, nil
}

// collectScenarioRequirements — steps 를 훑어 이식에 필요한 요구사항을 수집한다.
//   - app_macro step 의 macro.packageName → packages, macro.sourceWidth/Height → 해상도
//   - trace_start step 의 params.trace_type → traceType
func collectScenarioRequirements(steps []any) *scenarioRequirements {
	req := &scenarioRequirements{}
	pkgSeen := map[string]bool{}

	for _, s := range steps {
		step, ok := s.(map[string]any)
		if !ok {
			continue
		}
		stepType, _ := step["type"].(string)

		switch stepType {
		case "app_macro":
			macro, ok := step["macro"].(map[string]any)
			if !ok {
				continue
			}
			if pkg, ok := macro["packageName"].(string); ok && pkg != "" && !pkgSeen[pkg] {
				pkgSeen[pkg] = true
				req.Packages = append(req.Packages, pkg)
			}
			// 첫 해상도만 대표로 (보통 시나리오 내 동일 기기 기준)
			if req.SourceWidth == 0 {
				if w, ok := numberOf(macro["sourceWidth"]); ok && w > 0 {
					req.SourceWidth = int(w)
				}
			}
			if req.SourceHeight == 0 {
				if h, ok := numberOf(macro["sourceHeight"]); ok && h > 0 {
					req.SourceHeight = int(h)
				}
			}
		case "trace_start":
			if req.TraceType == "" {
				if params, ok := step["params"].(map[string]any); ok {
					if tt, ok := params["trace_type"].(string); ok && tt != "" {
						req.TraceType = tt
					}
				}
			}
		}
	}

	// 아무것도 없으면 nil 반환 (omitempty)
	if len(req.Packages) == 0 && req.SourceWidth == 0 && req.SourceHeight == 0 && req.TraceType == "" {
		return nil
	}
	return req
}

// scenarioContentHash — steps+loops 내용 해시. 중복 수입 스킵·변경 감지용.
//
// 입력 JSON 문자열을 파싱 후 재marshal 하여 정규화한다 — 키 순서·공백이 달라도
// 논리적으로 같은 내용이면 같은 해시가 나오게 한다. 이게 없으면 export(저장본 문자열)와
// import(재marshal) 의 해시가 어긋나 중복 스킵이 동작하지 않는다.
func scenarioContentHash(stepsJSON string, loopsJSON any) string {
	h := sha256.New()
	h.Write([]byte(normalizeJSON(stepsJSON)))
	if s, ok := loopsJSON.(string); ok && s != "" {
		h.Write([]byte("|"))
		h.Write([]byte(normalizeJSON(s)))
	}
	return "sha256:" + fmt.Sprintf("%x", h.Sum(nil))
}

// normalizeJSON — JSON 문자열을 파싱→재marshal 로 정규화. 파싱 실패 시 원본 반환.
// Go 의 json.Marshal 은 map 키를 알파벳순 정렬하므로 키 순서 차이가 흡수된다.
func normalizeJSON(s string) string {
	if s == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s // 파싱 불가면 원본 그대로 (best-effort)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(b)
}

// handleScenarioExport — GET /api/agent/scenario-templates/{id}/export
// 자체완결 JSON 을 attachment 로 다운로드시킨다.
func handleScenarioExport(w http.ResponseWriter, r *http.Request, db *sqlitedb.DB, id int64) {
	t, err := db.FindScenarioTemplate(r.Context(), id)
	if errors.Is(err, sqlitedb.ErrNotFound) {
		writeError(w, http.StatusNotFound, "template not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	exp, err := buildScenarioExport(t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	data, err := json.MarshalIndent(exp, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	filename := sanitizeFilename(t.Name) + ".scenario.json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// scenarioImportResult — import 응답. 생성된 템플릿 + 경고 리스트.
type scenarioImportResult struct {
	Imported []map[string]any `json:"imported"`
	Skipped  []string         `json:"skipped,omitempty"`  // 중복(contentHash 동일)으로 스킵된 이름
	Warnings []string         `json:"warnings,omitempty"` // 이식 시 주의(schemaVersion 등)
}

// handleScenarioImport — POST /api/agent/scenario-templates/import
// 단일 scenario 또는 배열(scenario-pack) 을 받아 검증 후 DB insert.
func handleScenarioImport(w http.ResponseWriter, r *http.Request, db *sqlitedb.DB) {
	raw, err := readRawBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read: "+err.Error())
		return
	}

	// 단일 객체 또는 배열 모두 수용
	imports, err := parseScenarioImports(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(imports) == 0 {
		writeError(w, http.StatusBadRequest, "no scenario to import")
		return
	}

	// 기존 템플릿들의 contentHash 수집 (중복 스킵용)
	existing, err := db.ListScenarioTemplates(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	existingHashes := map[string]bool{}
	for _, t := range existing {
		existingHashes[scenarioContentHash(t.StepsJSON, nullString(t.LoopsJSON))] = true
	}

	result := scenarioImportResult{Imported: []map[string]any{}}
	for _, exp := range imports {
		// schemaVersion 검증
		if exp.SchemaVersion == 0 {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%q: schemaVersion 없음 — 이식 파일이 아닐 수 있음", exp.Name))
		} else if exp.SchemaVersion > scenarioExportSchemaVersion {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%q: schemaVersion %d 는 이 agent(%d)보다 최신 — 일부 필드가 무시될 수 있음",
					exp.Name, exp.SchemaVersion, scenarioExportSchemaVersion))
		}
		if len(exp.Steps) == 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%q: steps 없음 — 스킵", exp.Name))
			continue
		}

		stepsJSON, _ := json.Marshal(exp.Steps)
		var loopsJSON string
		if len(exp.Loops) > 0 {
			b, _ := json.Marshal(exp.Loops)
			loopsJSON = string(b)
		}

		// 중복 스킵 (contentHash 동일)
		if existingHashes[scenarioContentHash(string(stepsJSON), loopsJSON)] {
			result.Skipped = append(result.Skipped, exp.Name)
			continue
		}

		// requirements 경고 (정보 제공: 미설치앱은 실행 시점에 걸리니 여기선 안내만)
		if exp.Requirements != nil && len(exp.Requirements.Packages) > 0 {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%q: 필요 앱 %s — 대상 기기에 설치돼 있어야 함",
					exp.Name, strings.Join(exp.Requirements.Packages, ", ")))
		}

		repeat := exp.RepeatCount
		if repeat <= 0 {
			repeat = 1
		}
		tmpl := &sqlitedb.ScenarioTemplate{
			Name:        exp.Name,
			Description: exp.Description,
			RepeatCount: repeat,
			StepsJSON:   string(stepsJSON),
			LoopsJSON:   nullStr(loopsJSON),
		}
		created, err := db.CreateScenarioTemplate(r.Context(), tmpl)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "create template: "+err.Error())
			return
		}
		result.Imported = append(result.Imported, scenarioTemplateToMap(created))
	}

	writeJSON(w, http.StatusOK, result)
}

// parseScenarioImports — body 를 단일 scenarioExport 또는 배열로 파싱.
func parseScenarioImports(raw []byte) ([]scenarioExport, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("empty body")
	}
	if trimmed[0] == '[' {
		var arr []scenarioExport
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, fmt.Errorf("decode scenario array: %w", err)
		}
		return arr, nil
	}
	var single scenarioExport
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, fmt.Errorf("decode scenario: %w", err)
	}
	return []scenarioExport{single}, nil
}

// handleScenarioExportAll — GET /api/agent/scenario-templates/export-all
// 모든 템플릿을 한 .scenariopack.json (배열) 으로 다운로드. 팀 배포·git 커밋용.
func handleScenarioExportAll(w http.ResponseWriter, r *http.Request, db *sqlitedb.DB) {
	list, err := db.ListScenarioTemplates(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	exports := make([]*scenarioExport, 0, len(list))
	for _, t := range list {
		exp, err := buildScenarioExport(t)
		if err != nil {
			// 개별 파싱 실패는 건너뛰되 전체는 계속
			continue
		}
		exports = append(exports, exp)
	}

	data, err := json.MarshalIndent(exports, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"scenarios.scenariopack.json\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// sanitizeFilename — 파일명에 부적합한 문자를 _ 로 치환. 빈 이름은 "scenario".
func sanitizeFilename(name string) string {
	if name == "" {
		return "scenario"
	}
	repl := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", " ", "_",
	)
	out := repl.Replace(name)
	// 제어문자 제거
	out = strings.Map(func(rr rune) rune {
		if rr < 0x20 {
			return -1
		}
		return rr
	}, out)
	if out == "" {
		return "scenario"
	}
	return out
}
