package server

import (
	"encoding/json"
	"testing"

	pb "agent/pb"
)

// traceStatsToMap 은 **손으로 쓴 allowlist** 라 새 필드가 자동으로 안 나간다.
// 키가 빠지면 화면에 아무것도 안 뜨는데 에러는 안 난다 — 여기서 고정한다.
func TestTraceStatsToMapCarriesAddressRange(t *testing.T) {
	s := &pb.TraceStats{
		AddressRange: []*pb.AddressRangeStats{
			{Direction: "all", MinAddr: 0, MaxAddr: 122138624, Span: 122138624, Count: 1220317, UnitBytes: 4096},
			{Direction: "read", MinAddr: 1048576, MaxAddr: 122138624, Span: 121090048, Count: 418204, UnitBytes: 4096},
		},
	}

	m := traceStatsToMap(s)

	raw, ok := m["addressRange"]
	if !ok {
		t.Fatal("addressRange 키가 없다 — allowlist 에 빠졌다")
	}
	list, ok := raw.([]map[string]any)
	if !ok || len(list) != 2 {
		t.Fatalf("addressRange = %#v, want 2개 항목", raw)
	}
	for k, want := range map[string]any{
		"direction": "all",
		"minAddr":   uint64(0),
		"maxAddr":   uint64(122138624),
		"span":      uint64(122138624),
		"count":     int64(1220317),
		// unitBytes 가 빠지면 화면이 UFS(4KB)와 Block(512B)을 같은 축으로 비교해
		// 8배 틀린 결론을 낸다. 키 존재만이 아니라 값까지 고정한다.
		"unitBytes": uint64(4096),
	} {
		if list[0][k] != want {
			t.Errorf("%s = %v(%T), want %v", k, list[0][k], list[0][k], want)
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
	arr, ok := back["addressRange"].([]any)
	if !ok {
		t.Fatalf("JSON 에서 addressRange 가 배열이 아니다: %T", back["addressRange"])
	}
	if len(arr) != 2 {
		t.Errorf("JSON 항목 수 = %d, want 2", len(arr))
	}
}

// 주소 범위가 없을 때(혼합 조회 / 구버전) null 이 아니라 빈 배열로 나가야 한다.
// null 이면 프론트가 `?? []` 를 해야 하는데, 이 코드베이스는 그걸 피하는 쪽을 택해 왔다.
func TestTraceStatsToMapEmptyAddressRangeIsArray(t *testing.T) {
	b, err := json.Marshal(traceStatsToMap(&pb.TraceStats{}))
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	v, ok := back["addressRange"].([]any)
	if !ok {
		t.Fatalf("빈 경우 배열이어야 한다, got %#v", back["addressRange"])
	}
	if len(v) != 0 {
		t.Errorf("빈 배열이어야 한다, got %d개", len(v))
	}
}
