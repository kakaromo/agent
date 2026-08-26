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
