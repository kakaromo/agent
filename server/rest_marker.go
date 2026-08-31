package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"agent/storage/sqlitedb"
	"agent/trace"
)

// trace_marker 기반 on-device AI 지표 REST.
//
//	POST /api/agent/marker/explore  {traceJobId, idleFrom?, idleTo?, runFrom?, runTo?}
//	POST /api/agent/marker/parse    {traceJobId, profileId? | patternsJson?}
//
// ⚠ logcat 쪽(`/api/agent/logcat/*`)과 **소스만 다르고 계약은 같다.** 저쪽은
// logcat.log, 이쪽은 trace 잡의 trace.log 를 읽는다. 화면이 두 소스를 같은 방식으로
// 다룰 수 있도록 응답 shape 도 맞췄다 (결과 타입이 LogcatParseResult 로 같다).
//
// ⚠⚠ **path 를 직접 받지 않는다.** logcat REST 는 path 를 받고 IsAllowedPath 로
// 걸렀는데, 여기서는 아예 `traceJobId` 만 받아 잡 폴더에서 파일명을 조립한다.
// 임의 경로가 들어올 여지 자체를 없애는 쪽이 가드를 얹는 것보다 안전하다
// (사무실 모드는 인증 없는 0.0.0.0 바인딩 위에 있다).
func registerMarkerRoutes(mux *http.ServeMux, tm *trace.Manager, db *sqlitedb.DB) {
	mux.HandleFunc("/api/agent/marker/explore", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		f, path, err := openTraceLog(body, tm)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		defer f.Close()

		res := trace.ExploreMarkers(f, trace.ExploreOptions{
			IdleFrom: floatOf(body["idleFrom"]), IdleTo: floatOf(body["idleTo"]),
			RunFrom: floatOf(body["runFrom"]), RunTo: floatOf(body["runTo"]),
		})
		writeJSON(w, http.StatusOK, map[string]any{"path": path, "result": res})
	})

	mux.HandleFunc("/api/agent/marker/parse", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		patternsJSON, err := resolveMarkerPatterns(r.Context(), body, db)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var pat trace.MarkerPatterns
		if err := json.Unmarshal([]byte(patternsJSON), &pat); err != nil {
			writeError(w, http.StatusBadRequest, "patternsJson: "+err.Error())
			return
		}

		f, path, err := openTraceLog(body, tm)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		defer f.Close()

		res, err := trace.ParseMarkerPatterns(f, &pat)
		if err != nil {
			// 패턴 자체가 잘못된 것은 사용자 입력 문제다 (400).
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// ⚠ 0건이어도 **200 으로 결과를 준다.** 에러로 만들면 "왜 0건인지" 진단이
		// 화면까지 못 간다 — 그게 이 기능의 핵심 산출물이다 (logcat 과 같은 판단).
		writeJSON(w, http.StatusOK, map[string]any{"path": path, "result": res})
	})
}

// openTraceLog — traceJobId 로 그 잡의 trace.log 를 연다.
//
// ⚠ 경로를 사용자에게서 받지 않는다. 잡 폴더(Dir)에 고정 파일명을 붙이므로
// traversal 이 성립할 수 없다.
func openTraceLog(body map[string]any, tm *trace.Manager) (*os.File, string, error) {
	if tm == nil {
		return nil, "", errText("trace manager 가 설정되지 않았다")
	}
	id, _ := body["traceJobId"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, "", errText("traceJobId 가 필요하다")
	}
	info, err := tm.GetTraceJobInfo(id)
	if err != nil {
		return nil, "", errText("trace 잡을 찾을 수 없다: " + id)
	}
	p := filepath.Join(info.Dir, "trace.log")
	f, err := os.Open(p)
	if err != nil {
		// ⚠ 원인을 갈라준다 — 파일이 없는 것과 권한 문제는 사용자가 할 일이 다르다.
		if os.IsNotExist(err) {
			return nil, "", errText("이 잡에는 trace.log 가 없다 (수집 방식이 다르거나 정리된 잡)")
		}
		return nil, "", errText("trace.log 를 열 수 없다: " + err.Error())
	}
	return f, p, nil
}

// resolveMarkerPatterns — profileId 또는 인라인 patternsJson 중 하나를 고른다.
//
// ⚠ profileId 로 저장된 프로파일을 쓸 때는 **소스가 marker 인지 확인한다.**
// logcat 프로파일을 marker 파싱에 넣으면 필드 이름이 달라 조용히 0건이 되는데,
// 사용자는 "패턴이 안 맞나" 로 시간을 쓴다 — 원인을 바로 말해주는 편이 낫다.
func resolveMarkerPatterns(ctx context.Context, body map[string]any, db *sqlitedb.DB) (string, error) {
	if raw, ok := body["patternsJson"]; ok {
		switch v := raw.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return v, nil
			}
		case map[string]any:
			b, err := json.Marshal(v)
			if err != nil {
				return "", errText("patternsJson 직렬화 실패")
			}
			return string(b), nil
		}
	}
	idf := floatOf(body["profileId"])
	if idf <= 0 {
		return "", errText("profileId 또는 patternsJson 이 필요하다")
	}
	if db == nil {
		return "", errText("profileId 로 조회하려면 DB 가 필요하다 (standalone 모드)")
	}
	prof, err := db.FindAILogProfile(ctx, int64(idf))
	if err != nil || prof == nil {
		return "", errText(fmt.Sprintf("프로파일 %d 을 찾을 수 없다", int64(idf)))
	}
	if prof.Source != "" && prof.Source != sqlitedb.AISourceMarker {
		return "", errText(fmt.Sprintf(
			"프로파일 %d 은 %s 용이다 — marker 파싱에는 쓸 수 없다 (조용히 0건이 된다)",
			int64(idf), prof.Source))
	}
	return prof.PatternsJSON, nil
}
