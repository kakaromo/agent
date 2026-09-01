package server

import (
	"encoding/json"
	"testing"

	pb "agent/pb"
)

// traceStatsToMap 은 **손으로 쓴 allowlist** 라 새 필드가 자동으로 안 나간다.
// 키가 빠지면 화면에 아무것도 안 뜨는데 에러는 안 난다 — 여기서 고정한다.
func TestTraceStatsToMapCarriesDirectionContiguity(t *testing.T) {
	s := &pb.TraceStats{
		SendCount:           10,
		ClassifiedSendCount: 8,
		DirectionContiguity: []*pb.DirectionContiguityStats{
			{
				Direction: "read", Contiguous: true, Count: 6,
				RatioWithinDirection: 75, RatioOfSends: 60,
				TotalBytes: 4096, AvgRequestBytes: 682.67,
			},
			{Direction: "read", Contiguous: false, Count: 2},
		},
	}

	m := traceStatsToMap(s)

	if got := m["classifiedSendCount"]; got != int64(8) {
		t.Errorf("classifiedSendCount = %v, want 8", got)
	}
	raw, ok := m["directionContiguity"]
	if !ok {
		t.Fatal("directionContiguity 키가 없다 — allowlist 에 빠졌다")
	}
	list, ok := raw.([]map[string]any)
	if !ok || len(list) != 2 {
		t.Fatalf("directionContiguity = %#v, want 2개 항목", raw)
	}
	e := list[0]
	for k, want := range map[string]any{
		"direction":            "read",
		"contiguous":           true,
		"count":                int64(6),
		"ratioWithinDirection": float64(75),
		"ratioOfSends":         float64(60),
		"totalBytes":           uint64(4096),
	} {
		if e[k] != want {
			t.Errorf("%s = %v(%T), want %v", k, e[k], e[k], want)
		}
	}

	// 프론트가 받는 JSON 이 실제로 그 모양인지.
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if _, ok := back["directionContiguity"].([]any); !ok {
		t.Errorf("JSON 에서 directionContiguity 가 배열이 아니다: %T", back["directionContiguity"])
	}
}

// 방향별 집계가 없을 때 null 이 아니라 빈 배열로 나가야 한다.
// null 이면 프론트가 `?? []` 를 해야 하는데, 이 코드베이스는 그걸 피하는 쪽을 택해 왔다.
func TestTraceStatsToMapEmptyDirectionContiguityIsArray(t *testing.T) {
	b, err := json.Marshal(traceStatsToMap(&pb.TraceStats{}))
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	v, ok := back["directionContiguity"].([]any)
	if !ok {
		t.Fatalf("빈 경우 배열이어야 한다, got %#v", back["directionContiguity"])
	}
	if len(v) != 0 {
		t.Errorf("빈 배열이어야 한다, got %d개", len(v))
	}
}
