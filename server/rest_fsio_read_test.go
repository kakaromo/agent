package server

import (
	"encoding/json"
	"testing"

	pb "agent/pb"
)

// 응답 JSON 계약.
//
// ⚠ optional 필드가 **없을 때 JSON 에서 빠져야** 한다. 0 으로 나가면:
//   - 비율: "전부 miss(0%)" 와 "판정할 게 없음" 이 구분되지 않는다
//   - 지연: "0ns 였다" 로 오독된다
//
// 프론트(TraceCacheView)는 없는 값을 "—" 로 렌더하는 것을 전제한다.
func TestFsioReadStatsToMapOmitsAbsentOptionals(t *testing.T) {
	resp := &pb.GetFsioReadStatsResponse{
		TotalRequests: 5,
		ByClass: []*pb.FsioReadClassStats{{
			CacheClass:      "UNKNOWN",
			Requests:        5,
			DurationSamples: 0, // 표본 없음 → 백분위 전부 nil
		}},
		SchemaVersion: "fsio_read-v1",
		// 비율 3종 전부 nil (판정 대상 0)
	}

	b, err := json.Marshal(fsioReadStatsToMap(resp))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}

	for _, k := range []string{"requestHitRatio", "requestMissRatio", "unknownRatio"} {
		if _, present := m[k]; present {
			t.Errorf("%s 가 응답에 있다 — 판정 대상이 0 이면 빠져야 한다(0 은 '전부 miss' 로 읽힌다)", k)
		}
	}
	cls := m["byClass"].([]any)[0].(map[string]any)
	for _, k := range []string{"durationAvgNs", "durationP50Ns", "durationP95Ns", "durationP99Ns"} {
		if _, present := cls[k]; present {
			t.Errorf("%s 가 응답에 있다 — 표본 0 이면 빠져야 한다('0ns 였다' 로 읽힌다)", k)
		}
	}
	// 경고/목록은 nil 이어도 [] 로 나가야 한다 (프론트가 .map 을 바로 돈다).
	for _, k := range []string{"qualityWarnings", "topFiles"} {
		if v, present := m[k]; !present || v == nil {
			t.Errorf("%s 는 빈 배열로 나가야 한다 (JSON null 금지)", k)
		}
	}
}

// 값이 있으면 그대로 실린다.
func TestFsioReadStatsToMapKeepsPresentValues(t *testing.T) {
	hit, miss, unk := 0.75, 0.25, 0.1
	avg, p50 := uint64(13151), uint64(8250)
	resp := &pb.GetFsioReadStatsResponse{
		TotalRequests:    363,
		RequestHitRatio:  &hit,
		RequestMissRatio: &miss,
		UnknownRatio:     &unk,
		ByClass: []*pb.FsioReadClassStats{{
			CacheClass: "CACHE_HIT_INFERRED", Requests: 232, DurationSamples: 232,
			DurationAvgNs: &avg, DurationP50Ns: &p50,
		}},
		TopFiles:        []*pb.FsioReadFileStats{{Key: "/data/ex/a.bin", Requests: 288, HitRequests: 192}},
		QualityWarnings: []string{"경고"},
		SchemaVersion:   "fsio_read-v1",
	}
	m := fsioReadStatsToMap(resp)
	if m["requestHitRatio"] != 0.75 {
		t.Errorf("requestHitRatio = %v, want 0.75", m["requestHitRatio"])
	}
	cls := m["byClass"].([]map[string]any)[0]
	if cls["durationAvgNs"] != uint64(13151) {
		t.Errorf("durationAvgNs = %v", cls["durationAvgNs"])
	}
	// p95/p99 는 안 넣었으니 여전히 빠져야 한다 (부분적으로만 있는 경우).
	if _, present := cls["durationP99Ns"]; present {
		t.Error("durationP99Ns 가 nil 인데 응답에 있다")
	}
	if m["schemaVersion"] != "fsio_read-v1" {
		t.Errorf("schemaVersion = %v", m["schemaVersion"])
	}
	if len(m["topFiles"].([]map[string]any)) != 1 {
		t.Error("topFiles 가 안 실렸다")
	}
}
