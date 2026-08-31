package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "agent/pb"
	"agent/storage/sqlitedb"
)

// ⚠⚠ logcat 과 marker 는 patterns_json 의 **필드 이름이 다르다**
// (marks/series vs counters/sections). 섞으면 JSON 파싱은 통과하는데 매칭이
// 조용히 0건이 된다 — 사용자는 정규식을 고쳐가며 헛수고한다.
func TestResolveMarkerPatterns_SourceGuard(t *testing.T) {
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	mk, err := db.CreateAILogProfile(ctx, &sqlitedb.AILogProfile{
		Name: "m", Runtime: "qnn", Source: sqlitedb.AISourceMarker,
		PatternsJSON: `{"counters":[{"key":"tpot","name":"decode_ms_per_token"}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	lg, err := db.CreateAILogProfile(ctx, &sqlitedb.AILogProfile{
		Name: "l", Runtime: "x",
		PatternsJSON: `{"series":[{"key":"ttft","regex":"TTFT ([0-9.]+)"}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("marker 프로파일은 통과", func(t *testing.T) {
		got, err := resolveMarkerPatterns(ctx, map[string]any{"profileId": float64(mk.ID)}, db)
		if err != nil {
			t.Fatalf("marker 프로파일이 거부됐다: %v", err)
		}
		if !strings.Contains(got, "counters") {
			t.Errorf("패턴이 안 나왔다: %s", got)
		}
	})

	t.Run("logcat 프로파일은 차단 — 조용한 0건 대신 원인을 말한다", func(t *testing.T) {
		_, err := resolveMarkerPatterns(ctx, map[string]any{"profileId": float64(lg.ID)}, db)
		if err == nil {
			t.Fatal("logcat 프로파일이 marker 파싱에 통과했다 — 조용히 0건이 된다")
		}
		if !strings.Contains(err.Error(), "logcat") {
			t.Errorf("원인이 안 담겼다: %v", err)
		}
	})

	t.Run("인라인 patternsJson 이 우선", func(t *testing.T) {
		got, err := resolveMarkerPatterns(ctx, map[string]any{
			"profileId":    float64(lg.ID),
			"patternsJson": map[string]any{"counters": []any{}},
		}, db)
		if err != nil {
			t.Fatalf("인라인이 거부됐다: %v", err)
		}
		if !strings.Contains(got, "counters") {
			t.Errorf("인라인 값이 안 쓰였다: %s", got)
		}
	})
}

// ⚠ 저장 시 소스별로 다른 검증기를 타야 한다. 안 갈라주면 marker 패턴이
// logcat 검증(캡처 그룹 필수)에 걸려 저장이 거부된다.
func TestCreateAILogProfile_MarkerValidation(t *testing.T) {
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	// 캡처 그룹이 없어도 marker 는 정상이다 (값은 C| 의 마지막 필드에서 온다).
	if _, err := db.CreateAILogProfile(ctx, &sqlitedb.AILogProfile{
		Name: "m", Runtime: "q", Source: sqlitedb.AISourceMarker,
		PatternsJSON: `{"counters":[{"key":"tpot","name":"decode_ms_per_token"}]}`,
	}); err != nil {
		t.Errorf("marker 패턴이 거부됐다: %v", err)
	}
	// name/regex 둘 다 없으면 무엇을 찾을지 알 수 없다.
	if _, err := db.CreateAILogProfile(ctx, &sqlitedb.AILogProfile{
		Name: "bad", Runtime: "q", Source: sqlitedb.AISourceMarker,
		PatternsJSON: `{"counters":[{"key":"x"}]}`,
	}); err == nil {
		t.Error("name/regex 없는 marker 패턴이 통과했다")
	}
	// 빈 source 는 logcat 으로 정규화 — 기존 프로파일 호환.
	p, err := db.CreateAILogProfile(ctx, &sqlitedb.AILogProfile{
		Name: "l", Runtime: "q",
		PatternsJSON: `{"series":[{"key":"t","regex":"x ([0-9]+)"}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != sqlitedb.AISourceLogcat {
		t.Errorf("빈 source 가 %q 로 저장됐다 — logcat 이어야 한다", p.Source)
	}
}

// ⚠ 검증 메시지가 전부 400 으로 분류돼야 한다. 하나라도 500 이 나가면 사용자는
// 서버 탓으로 읽고 자기 패턴을 고칠 생각을 못 한다 (이 함수의 존재 이유).
// 반대로 매칭이 너무 넓으면 진짜 서버 에러가 400 으로 둔갑한다.
func TestStatusForProfileErr_MarkerMessages(t *testing.T) {
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	// marker 검증이 거부하는 모든 경우를 실제로 태워 본다.
	bad := []string{
		`{}`,                                     // counters/sections 둘 다 없음
		`{"counters":[{"name":"x"}]}`,            // key 없음
		`{"counters":[{"key":"a"}]}`,             // name/regex 둘 다 없음
		`{"counters":[{"key":"a","regex":"("}]}`, // 잘못된 정규식
		`{"counters":[{"key":"a","name":"x"}],"sections":[{"key":"a","name":"y"}]}`, // 키 중복
	}
	for _, pj := range bad {
		_, err := db.CreateAILogProfile(ctx, &sqlitedb.AILogProfile{
			Name: "n", Runtime: "r", Source: sqlitedb.AISourceMarker, PatternsJSON: pj,
		})
		if err == nil {
			t.Errorf("검증을 통과했다: %s", pj)
			continue
		}
		if got := statusForProfileErr(err); got != 400 {
			t.Errorf("%s → HTTP %d (400 이어야 한다): %v", pj, got, err)
		}
	}

	// 서버 에러는 500 이어야 한다.
	//
	// ⚠ 영어 DB 에러만으로는 검증이 안 된다 — 한국어 문구와 겹칠 일이 없어서
	// 매칭이 아무리 넓어도 통과한다. **한국어가 섞인 서버 에러**를 넣어야 넓은 매칭이
	// 드러난다 (이 코드베이스는 에러 메시지를 한국어로 쓴다).
	for _, msg := range []string{
		"database is locked",
		"disk I/O error",
		"잡 폴더를 만들 수 없다: 디스크 공간이 비어 있다",      // "비어 있다" 를 담은 서버 에러
		"archive 경로가 설정돼 있어야 한다 (설정 파일 확인)", // "있어야 한다" 를 담은 서버 에러
	} {
		if got := statusForProfileErr(errText(msg)); got != 500 {
			t.Errorf("서버 에러 %q 가 HTTP %d 로 분류됐다 — 사용자가 자기 패턴 탓으로 오해한다", msg, got)
		}
	}
}

// ⚠⚠ 라이브 응답(rest_convert)과 영속화(rest_hook)가 **같은 변환**을 써야 한다.
//
// 예전엔 필드 목록을 각자 들고 있었는데, 새 필드를 한쪽에만 넣는 바람에 잡이 만료된
// 뒤에만 구간이 사라지는 버그가 났다 — 라이브로 확인하면 정상이라 발견이 늦다.
func TestStepBoundaryConversionIsShared(t *testing.T) {
	src, err := os.ReadFile("rest_hook.go")
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(src), "func collectStepBoundariesFrom(")
	if i < 0 {
		t.Fatal("collectStepBoundariesFrom 을 찾지 못했다 — 테스트가 낡았다")
	}
	body := string(src)[i:]
	if j := strings.Index(body[1:], "\nfunc "); j > 0 {
		body = body[:j+1]
	}
	if !strings.Contains(body, "stepBoundaryToMap(") {
		t.Error("영속화가 공용 변환을 안 쓴다 — 필드가 갈라지면 만료된 잡에서만 구간이 사라진다")
	}
	// 필드 목록을 자체적으로 들고 있으면 안 된다.
	if strings.Contains(body, `"startedMono"`) {
		t.Error("영속화가 필드 목록을 따로 갖고 있다 — 공용 변환을 쓸 것")
	}
}

// 새 marker 필드가 변환에 실제로 실리는지 (proto 에만 넣고 빠뜨리는 사고 방지).
func TestStepBoundaryToMapCarriesMarkerTimes(t *testing.T) {
	m := stepBoundaryToMap(&pb.StepBoundary{
		StartedMono: 100, FinishedMono: 101,
		MarkerStartedMono: 200, MarkerFinishedMono: 201,
	})
	for _, k := range []string{"startedMono", "finishedMono", "markerStartedMono", "markerFinishedMono"} {
		if _, ok := m[k]; !ok {
			t.Errorf("%q 가 변환에서 빠졌다 — 화면이 드리프트 시 쓸 값을 못 받는다", k)
		}
	}
	if m["markerStartedMono"] != float64(200) {
		t.Errorf("markerStartedMono=%v", m["markerStartedMono"])
	}
}
