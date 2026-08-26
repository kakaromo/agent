package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"agent/storage/sqlitedb"
	"agent/trace"
)

const fixtureLog = `9571.204   900   900 I Genie   : model load start
9573.184   900   900 I Genie   : model load done (1980 ms)
9574.044   900   900 I Genie   : first token emitted — TTFT 2840 ms
9574.068   900   900 I Genie   : decode 24.1 ms/tok
9574.092   900   900 I Genie   : decode 23.8 ms/tok`

func newLogcatMux(t *testing.T) (*http.ServeMux, string, *sqlitedb.DB) {
	t.Helper()
	base := t.TempDir()
	lm := trace.NewLogcatManager(nil, base)
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	logPath := filepath.Join(base, "logcat.log")
	if err := os.WriteFile(logPath, []byte(fixtureLog), 0644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerLogcatRoutes(mux, lm, db)
	return mux, logPath, db
}

func TestLogcatREST_Explore(t *testing.T) {
	mux, logPath, _ := newLogcatMux(t)
	w := do(t, mux, "POST", "/api/agent/logcat/explore",
		`{"path":`+strconv.Quote(logPath)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("explore = %d: %s", w.Code, w.Body)
	}
	var out struct {
		Result trace.ExploreResult `json:"result"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Result.Candidates) == 0 {
		t.Fatal("후보가 없다")
	}
	if out.Result.Candidates[0].Tag != "Genie" {
		t.Errorf("1위 = %q", out.Result.Candidates[0].Tag)
	}
	if out.Result.WeakOnly {
		t.Error("진짜 LLM 지표가 있는데 WeakOnly 다")
	}
}

func TestLogcatREST_Parse(t *testing.T) {
	mux, logPath, _ := newLogcatMux(t)
	patterns := `{"series":[{"key":"ttft_ms","regex":"TTFT ([0-9.]+) ms"},` +
		`{"key":"tpot_ms","regex":"decode ([0-9.]+) ms/tok"}]}`
	w := do(t, mux, "POST", "/api/agent/logcat/parse",
		`{"path":`+strconv.Quote(logPath)+`,"patternsJson":`+strconv.Quote(patterns)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("parse = %d: %s", w.Code, w.Body)
	}
	var out struct {
		Result trace.LogcatParseResult `json:"result"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Result.Series["ttft_ms"].Points[0].Value != 2840 {
		t.Errorf("ttft = %+v", out.Result.Series["ttft_ms"])
	}
	if out.Result.Series["tpot_ms"].Count != 2 {
		t.Errorf("tpot count = %d, 기대 2", out.Result.Series["tpot_ms"].Count)
	}
}

// profileId 로도 파싱할 수 있어야 한다 (저장된 프로파일 재사용).
func TestLogcatREST_ParseByProfileID(t *testing.T) {
	mux, logPath, db := newLogcatMux(t)
	p, err := db.CreateAILogProfile(t.Context(), &sqlitedb.AILogProfile{
		Name: "p", Runtime: "qnn",
		PatternsJSON: `{"series":[{"key":"ttft_ms","regex":"TTFT ([0-9.]+) ms"}]}`})
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, mux, "POST", "/api/agent/logcat/parse",
		`{"path":`+strconv.Quote(logPath)+`,"profileId":`+strconv.FormatInt(p.ID, 10)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("parse = %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "2840") {
		t.Errorf("프로파일 패턴이 적용되지 않았다: %s", w.Body)
	}
}

// ⚠ 0건이어도 200 이어야 한다. 에러로 만들면 "왜 0건인지" 진단이 화면까지 못 간다.
func TestLogcatREST_ZeroMatchStillReturnsDiagnosis(t *testing.T) {
	mux, logPath, _ := newLogcatMux(t)
	w := do(t, mux, "POST", "/api/agent/logcat/parse",
		`{"path":`+strconv.Quote(logPath)+`,"patternsJson":`+
			strconv.Quote(`{"series":[{"key":"x","regex":"NOPE ([0-9]+)"}]}`)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("0건인데 %d 를 줬다 — 진단이 화면에 못 간다: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "0건 매칭") {
		t.Errorf("진단이 없다: %s", w.Body)
	}
}

// ⚠⚠ 보안 가드. path 를 그대로 열면 서버의 아무 파일이나 노출된다.
func TestLogcatREST_PathTraversalBlocked(t *testing.T) {
	mux, _, _ := newLogcatMux(t)
	for _, p := range []string{"/etc/passwd", "/etc/hosts"} {
		w := do(t, mux, "POST", "/api/agent/logcat/explore", `{"path":`+strconv.Quote(p)+`}`)
		if w.Code == http.StatusOK {
			t.Errorf("허용 루트 밖 경로 %q 가 읽혔다", p)
		}
	}
	// jobId/path 둘 다 없으면 400
	if w := do(t, mux, "POST", "/api/agent/logcat/explore", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("입력 없음 = %d, 기대 400", w.Code)
	}
	// 패턴 없이 parse 하면 400
	if w := do(t, mux, "POST", "/api/agent/logcat/parse", `{"path":"x"}`); w.Code != http.StatusBadRequest {
		t.Errorf("패턴 없음 = %d, 기대 400", w.Code)
	}
}

func TestLogcatREST_MethodNotAllowed(t *testing.T) {
	mux, _, _ := newLogcatMux(t)
	if w := do(t, mux, "GET", "/api/agent/logcat/explore", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, 기대 405", w.Code)
	}
}
