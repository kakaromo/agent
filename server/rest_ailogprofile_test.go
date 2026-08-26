package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"agent/storage/sqlitedb"
)

func newProfileMux(t *testing.T) (*http.ServeMux, *sqlitedb.DB) {
	t.Helper()
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mux := http.NewServeMux()
	registerAILogProfileRoutes(mux, db)
	return mux, db
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

const okPatterns = `{"tags":["Genie"],"series":[{"key":"ttft_ms","regex":"TTFT ([0-9.]+) ms","unit":"ms"}]}`

func TestAILogProfileREST_CRUD(t *testing.T) {
	mux, _ := newProfileMux(t)

	// 빈 목록은 null 이 아니라 [] 여야 한다 (프론트가 .map 을 바로 돈다).
	w := do(t, mux, "GET", "/api/agent/ai-log-profiles", "")
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("빈 목록 = %q, 기대 []", got)
	}

	// 생성 — patternsJson 을 문자열로
	w = do(t, mux, "POST", "/api/agent/ai-log-profiles",
		`{"name":"QNN/Genie","runtime":"qnn","soc":"SM8975","patternsJson":`+
			strconv.Quote(okPatterns)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create = %d: %s", w.Code, w.Body)
	}
	var created map[string]any
	json.Unmarshal(w.Body.Bytes(), &created)
	id := int64(created["id"].(float64))
	if created["runtime"] != "qnn" || created["soc"] != "SM8975" {
		t.Errorf("create 응답 = %+v", created)
	}

	// 단건 조회
	w = do(t, mux, "GET", "/api/agent/ai-log-profiles/"+strconv.FormatInt(id, 10), "")
	if w.Code != http.StatusOK {
		t.Errorf("get = %d: %s", w.Code, w.Body)
	}

	// 수정
	w = do(t, mux, "PUT", "/api/agent/ai-log-profiles/"+strconv.FormatInt(id, 10),
		`{"name":"renamed","runtime":"qnn","patternsJson":`+strconv.Quote(okPatterns)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", w.Code, w.Body)
	}
	var updated map[string]any
	json.Unmarshal(w.Body.Bytes(), &updated)
	if updated["name"] != "renamed" {
		t.Errorf("update 결과 = %+v", updated)
	}

	// 삭제 + 재삭제는 404
	if w = do(t, mux, "DELETE", "/api/agent/ai-log-profiles/"+strconv.FormatInt(id, 10), ""); w.Code != http.StatusOK {
		t.Errorf("delete = %d", w.Code)
	}
	if w = do(t, mux, "DELETE", "/api/agent/ai-log-profiles/"+strconv.FormatInt(id, 10), ""); w.Code != http.StatusNotFound {
		t.Errorf("재삭제 = %d, 기대 404", w.Code)
	}
	if w = do(t, mux, "GET", "/api/agent/ai-log-profiles/9999", ""); w.Code != http.StatusNotFound {
		t.Errorf("없는 id = %d, 기대 404", w.Code)
	}
	if w = do(t, mux, "GET", "/api/agent/ai-log-profiles/abc", ""); w.Code != http.StatusBadRequest {
		t.Errorf("잘못된 id = %d, 기대 400", w.Code)
	}
}

// patternsJson 을 객체로 보내도 받아야 한다 (UI 는 객체가 자연스럽다).
func TestAILogProfileREST_ObjectPatterns(t *testing.T) {
	mux, _ := newProfileMux(t)
	w := do(t, mux, "POST", "/api/agent/ai-log-profiles",
		`{"name":"x","runtime":"llamacpp","patternsJson":`+okPatterns+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("객체 형태가 거부됐다 = %d: %s", w.Code, w.Body)
	}
	var m map[string]any
	json.Unmarshal(w.Body.Bytes(), &m)
	if s, _ := m["patternsJson"].(string); !strings.Contains(s, "ttft_ms") {
		t.Errorf("patternsJson 이 문자열로 저장되지 않았다: %v", m["patternsJson"])
	}
}

// ⚠ 검증 실패는 400 이어야 한다. 500 을 주면 서버 탓처럼 보여서 사용자가
// 자기 패턴을 고칠 생각을 못 한다.
func TestAILogProfileREST_BadPatternsAre400(t *testing.T) {
	mux, _ := newProfileMux(t)
	cases := map[string]string{
		"잘못된 정규식":  `{"name":"a","runtime":"r","patternsJson":"{\"marks\":[{\"key\":\"k\",\"regex\":\"([bad\"}]}"}`,
		"캡처 그룹 없음": `{"name":"a","runtime":"r","patternsJson":"{\"series\":[{\"key\":\"k\",\"regex\":\"TTFT [0-9]+ ms\"}]}"}`,
		"패턴 0개":    `{"name":"a","runtime":"r","patternsJson":"{\"tags\":[\"X\"]}"}`,
		"필수 필드 누락": `{"name":"a"}`,
	}
	for name, body := range cases {
		w := do(t, mux, "POST", "/api/agent/ai-log-profiles", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, 기대 400 (body: %s)", name, w.Code, w.Body)
		}
	}
}

// runtime/soc 필터 — 컬럼으로 뺀 이유가 이거다.
func TestAILogProfileREST_Filter(t *testing.T) {
	mux, _ := newProfileMux(t)
	mk := func(name, runtime, soc string) {
		body := `{"name":"` + name + `","runtime":"` + runtime + `","soc":"` + soc +
			`","patternsJson":` + strconv.Quote(okPatterns) + `}`
		if w := do(t, mux, "POST", "/api/agent/ai-log-profiles", body); w.Code != http.StatusOK {
			t.Fatalf("seed %s 실패: %s", name, w.Body)
		}
	}
	mk("qnn-8975", "qnn", "SM8975")
	mk("qnn-any", "qnn", "") // soc 빈 값 = 런타임 공용
	mk("mtk", "neuropilot", "MT6989")

	count := func(q string) int {
		w := do(t, mux, "GET", "/api/agent/ai-log-profiles"+q, "")
		var list []map[string]any
		json.Unmarshal(w.Body.Bytes(), &list)
		return len(list)
	}
	if n := count("?runtime=qnn"); n != 2 {
		t.Errorf("runtime=qnn → %d개, 기대 2", n)
	}
	// ⚠ soc 를 물으면 **빈 soc(공용) 프로파일도 포함**돼야 한다.
	// 배제하면 "이 AP 용" 을 물었을 때 공용 프로파일을 못 쓰게 된다.
	if n := count("?soc=SM8975"); n != 2 {
		t.Errorf("soc=SM8975 → %d개, 기대 2 (전용 1 + 공용 1)", n)
	}
	if n := count("?runtime=QNN"); n != 2 {
		t.Errorf("대소문자 무시가 안 된다 → %d개", n)
	}
	if n := count(""); n != 3 {
		t.Errorf("필터 없음 → %d개, 기대 3", n)
	}
}

func TestAILogProfileREST_MethodNotAllowed(t *testing.T) {
	mux, _ := newProfileMux(t)
	if w := do(t, mux, "PATCH", "/api/agent/ai-log-profiles", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("PATCH = %d, 기대 405", w.Code)
	}
}
