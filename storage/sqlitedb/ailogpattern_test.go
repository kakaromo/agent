package sqlitedb

import "testing"

// ValidatePatternsJSON 이 실제로 **거절해야 할 것을 거절하는지** 본다.
// 통과 케이스만 보면 검증이 아니다 — 아래 rejects 는 전부 "이걸 통과시키면
// 측정 시점에 조용히 틀린다" 는 종류다.
func TestValidatePatternsJSON(t *testing.T) {
	valid := `{"tags":["Genie"],
		"marks":[{"key":"prefill_begin","regex":"prefill begin"}],
		"series":[{"key":"ttft_ms","regex":"TTFT ([0-9.]+) ms","unit":"ms"}]}`
	if err := ValidatePatternsJSON(valid); err != nil {
		t.Fatalf("정상 프로파일이 거절됐다: %v", err)
	}

	// marks 만 있어도 유효하다 (구간 경계만 쓰는 프로파일).
	if err := ValidatePatternsJSON(`{"marks":[{"key":"a","regex":"x"}]}`); err != nil {
		t.Fatalf("marks 전용 프로파일이 거절됐다: %v", err)
	}

	rejects := []struct {
		name string
		json string
	}{
		{"빈 문자열", ``},
		{"깨진 JSON", `{"marks":`},
		{"패턴 0개 — 매칭이 항상 0건이라 잡이 무조건 실패한다",
			`{"tags":["Genie"]}`},
		{"잘못된 정규식 — 측정 시점에 터진다",
			`{"marks":[{"key":"a","regex":"([unclosed"}]}`},
		{"series 에 캡처 그룹 없음 — 매칭은 되는데 값이 안 나온다",
			`{"series":[{"key":"ttft_ms","regex":"TTFT [0-9.]+ ms"}]}`},
		{"key 중복 — 나중 것이 앞의 것을 덮어써 한쪽이 조용히 사라진다",
			`{"marks":[{"key":"dup","regex":"a"}],"series":[{"key":"dup","regex":"b([0-9])"}]}`},
		{"key 없음", `{"marks":[{"regex":"x"}]}`},
		{"regex 없음", `{"marks":[{"key":"a"}]}`},
	}
	for _, tc := range rejects {
		if err := ValidatePatternsJSON(tc.json); err == nil {
			t.Errorf("거절해야 하는데 통과됐다: %s", tc.name)
		}
	}
}
