package sqlitedb

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAILogProfileCRUD(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	pat := `{"tags":["Genie"],"series":[{"key":"ttft_ms","regex":"TTFT ([0-9.]+) ms"}]}`
	c, err := db.CreateAILogProfile(ctx, &AILogProfile{
		Name: "QNN/Genie", Runtime: "qnn", SOC: "SM8975", PatternsJSON: pat})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == 0 || c.SOC != "SM8975" || c.CreatedAt.IsZero() {
		t.Fatalf("create 결과가 이상하다: %+v", c)
	}
	// soc 를 비운 경우 (런타임 공용) 도 되는지
	if _, err := db.CreateAILogProfile(ctx, &AILogProfile{
		Name: "llama.cpp", Runtime: "llamacpp", PatternsJSON: pat}); err != nil {
		t.Fatalf("soc 빈 값 create 실패: %v", err)
	}
	// 잘못된 정규식은 저장 단계에서 막혀야 한다
	if _, err := db.CreateAILogProfile(ctx, &AILogProfile{
		Name: "bad", Runtime: "x", PatternsJSON: `{"marks":[{"key":"a","regex":"([bad"}]}`}); err == nil {
		t.Error("잘못된 정규식이 저장됐다")
	}
	list, _ := db.ListAILogProfiles(ctx)
	if len(list) != 2 {
		t.Fatalf("list 개수 %d, 기대 2", len(list))
	}
	u, err := db.UpdateAILogProfile(ctx, c.ID, &AILogProfile{
		Name: "renamed", Runtime: "qnn", SOC: "SM8975", PatternsJSON: pat})
	if err != nil || u.Name != "renamed" {
		t.Fatalf("update: %v %+v", err, u)
	}
	if _, err := db.UpdateAILogProfile(ctx, 9999, &AILogProfile{
		Name: "x", Runtime: "y", PatternsJSON: pat}); err != ErrNotFound {
		t.Errorf("없는 id update 는 ErrNotFound 여야 한다: %v", err)
	}
	if err := db.DeleteAILogProfile(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAILogProfile(ctx, c.ID); err != ErrNotFound {
		t.Errorf("두 번째 delete 는 ErrNotFound 여야 한다: %v", err)
	}
}

// ⚠ Update 가 필수 검사를 건너뛰면 patternsJson 만 담긴 PUT 이 name/runtime 을
// 빈 값으로 덮어쓴다. runtime 은 조회 필터 컬럼이라(`GET ?runtime=`) 비면 그
// 프로파일이 목록에서 조용히 사라진 것처럼 보인다.
func TestUpdateAILogProfileRequiresFields(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	created, err := db.CreateAILogProfile(ctx, &AILogProfile{
		Name: "keep", Runtime: "qnn",
		PatternsJSON: `{"series":[{"key":"ttft","regex":"TTFT ([0-9.]+)"}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	// name/runtime 을 비운 부분 업데이트는 거절돼야 한다.
	_, err = db.UpdateAILogProfile(ctx, created.ID, &AILogProfile{
		PatternsJSON: `{"series":[{"key":"ttft","regex":"TTFT ([0-9.]+)"}]}`,
	})
	if err == nil {
		t.Error("name/runtime 없이 통과했다 — 필터 컬럼이 비어 프로파일이 안 보이게 된다")
	}
	// 원본이 살아 있어야 한다.
	got, err := db.FindAILogProfile(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "keep" || got.Runtime != "qnn" {
		t.Errorf("원본이 덮어써졌다: name=%q runtime=%q", got.Name, got.Runtime)
	}
}
