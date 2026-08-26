package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"agent/storage/sqlitedb"
	"agent/trace"
)

// logcat 탐색/파싱 REST.
//
//	POST /api/agent/logcat/explore  {jobId?, path?, idleFrom, idleTo, runFrom, runTo}
//	POST /api/agent/logcat/parse    {jobId?, path?, profileId? | patternsJson?}
//
// ⚠ 수집 시작/중지는 여기 없다. logcat 은 **잡 옵션**(logcat=on)으로 켜지므로
// 시나리오 실행 경로가 수집을 관장한다. 여기는 이미 수집된 로그를 읽는 쪽이다.
func registerLogcatRoutes(mux *http.ServeMux, lm *trace.LogcatManager, db *sqlitedb.DB) {
	mux.HandleFunc("/api/agent/logcat/explore", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		f, path, err := openLogcatSource(body, lm)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		defer f.Close()

		res := trace.ExploreLogcat(f, trace.ExploreOptions{
			IdleFrom: floatOf(body["idleFrom"]), IdleTo: floatOf(body["idleTo"]),
			RunFrom: floatOf(body["runFrom"]), RunTo: floatOf(body["runTo"]),
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"path": path, "result": res,
		})
	})

	mux.HandleFunc("/api/agent/logcat/parse", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}

		patternsJSON, err := resolvePatterns(r, body, db)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		pat, err := trace.ParseLogcatPatternsJSON(patternsJSON)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		f, path, err := openLogcatSource(body, lm)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		defer f.Close()

		res, err := trace.ParseLogcat(f, pat)
		if err != nil {
			// 패턴 자체가 잘못된 것은 사용자 입력 문제다 (400).
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// ⚠ 0건이어도 **200 으로 결과를 준다.** 에러로 만들면 진단 메시지가
		// 화면까지 못 간다 — "왜 0건인지" 가 이 기능의 핵심 산출물이다.
		// 실패 판정은 호출자가 totalHits/partial 을 보고 한다.
		writeJSON(w, http.StatusOK, map[string]any{
			"path": path, "result": res,
		})
	})
}

// openLogcatSource — jobId 또는 path 로 로그 파일을 연다.
//
// ⚠ path 를 그대로 열지 않는다. 임의 경로를 읽게 두면 서버의 아무 파일이나
// 노출된다 — logcat 산출물 루트 안으로 제한한다.
func openLogcatSource(body map[string]any, lm *trace.LogcatManager) (*os.File, string, error) {
	if id, _ := body["jobId"].(string); strings.TrimSpace(id) != "" {
		if lm == nil {
			return nil, "", errText("logcat manager 가 설정되지 않았다")
		}
		job, err := lm.GetLogcatJob(strings.TrimSpace(id))
		if err != nil {
			return nil, "", err
		}
		f, err := os.Open(job.LogFile)
		if err != nil {
			return nil, "", errText("로그 파일을 열 수 없다: " + err.Error())
		}
		return f, job.LogFile, nil
	}

	p, _ := body["path"].(string)
	p = strings.TrimSpace(p)
	if p == "" {
		return nil, "", errText("jobId 또는 path 가 필요하다")
	}
	if lm == nil {
		return nil, "", errText("logcat manager 가 설정되지 않았다")
	}
	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return nil, "", errText("경로 해석 실패")
	}
	if !lm.IsAllowedPath(abs) {
		// 어떤 경로가 허용되는지는 알려주지 않는다 (내부 구조 노출).
		return nil, "", errText("허용된 logcat 산출물 경로가 아니다")
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, "", errText("로그 파일을 열 수 없다: " + err.Error())
	}
	return f, abs, nil
}

// resolvePatterns — profileId 또는 인라인 patternsJson 에서 패턴을 얻는다.
func resolvePatterns(r *http.Request, body map[string]any, db *sqlitedb.DB) (string, error) {
	if v, ok := body["patternsJson"].(string); ok && strings.TrimSpace(v) != "" {
		return v, nil
	}
	if id := int64Of(body["profileId"]); id > 0 {
		if db == nil {
			return "", errText("profileId 를 쓰려면 DB 가 필요하다 (standalone 모드)")
		}
		p, err := db.FindAILogProfile(r.Context(), id)
		if err != nil {
			return "", errText("프로파일을 찾을 수 없다")
		}
		return p.PatternsJSON, nil
	}
	return "", errText("profileId 또는 patternsJson 이 필요하다")
}

type errText string

func (e errText) Error() string { return string(e) }

func floatOf(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func int64Of(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}
