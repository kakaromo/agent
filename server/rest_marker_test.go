package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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
