package trace

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"agent/trace/parser"
)

// 기대값은 Rust `../trace/tests/dram_bw_log.rs` 와 **같다** — 같은 fixture 에서 두 구현이
// 같은 숫자를 내야 한다 (화면 두 개가 다른 말을 하면 안 된다).
const dramFixtureT0 = 268879.626314

func dramJob(t *testing.T, mutate func([]parser.DramBwEvent)) []*TraceJobInfo {
	t.Helper()
	ev, err := parser.ParseDramLog(filepath.Join("parser", "testdata", "dram_bw_sample.log"))
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(ev)
	}
	dir := t.TempDir()
	if err := parser.WriteDramBwParquet(ev, dir); err != nil {
		t.Fatal(err)
	}
	return []*TraceJobInfo{{Dir: dir, TraceType: "ufs"}}
}

func TestDramAggSummaryMatchesRust(t *testing.T) {
	r, err := ComputeDramBw(dramJob(t, nil), DramBwQuery{})
	if err != nil {
		t.Fatal(err)
	}
	s := r.Summary
	if !r.Available || s.Samples != 10 {
		t.Fatalf("available=%v samples=%d", r.Available, s.Samples)
	}
	// 단순 평균 5078/10 — us 가중 없음, MiB/s 무변환.
	if math.Abs(*s.AvgMibps-507.8) > 1e-9 {
		t.Errorf("avg=%v want 507.8", *s.AvgMibps)
	}
	if *s.MinMibps != 1 || *s.MaxMibps != 1200 || *s.P50Mibps != 469 || *s.P95Mibps != 1200 {
		t.Errorf("min/max/p50/p95 = %d/%d/%d/%d", *s.MinMibps, *s.MaxMibps, *s.P50Mibps, *s.P95Mibps)
	}
	if math.Abs(*s.IntervalMedianMs-4.0) > 1e-3 {
		t.Errorf("interval median=%v", *s.IntervalMedianMs)
	}
	if r.Bucketed || len(r.MaxMibps) != 10 || len(r.QualityWarnings) != 0 {
		t.Errorf("bucketed=%v pts=%d warns=%v", r.Bucketed, len(r.MaxMibps), r.QualityWarnings)
	}
	if len(r.Devs) != 1 || r.Devs[0] != "3109100.qcom,bwmon-ddr" {
		t.Errorf("devs=%v", r.Devs)
	}
}

func TestDramAggNotWeightedByUs(t *testing.T) {
	r, err := ComputeDramBw(dramJob(t, func(ev []parser.DramBwEvent) {
		for i := range ev {
			if i%2 == 1 {
				ev[i].Us = 40000
			}
		}
	}), DramBwQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(*r.Summary.AvgMibps-507.8) > 1e-9 {
		t.Errorf("us 로 가중됐다: avg=%v", *r.Summary.AvgMibps)
	}
	if len(r.QualityWarnings) == 0 || !strings.Contains(r.QualityWarnings[0], "us") {
		t.Errorf("us 불일치 경고가 없다: %v", r.QualityWarnings)
	}
}

func TestDramAggSpans(t *testing.T) {
	spans := []DramSpan{
		{Start: dramFixtureT0 - 0.0001, End: dramFixtureT0 + 0.0081}, // 532,469,300
		{Start: dramFixtureT0 + 0.0319, End: dramFixtureT0 + 0.0361}, // 266,500
	}
	r, err := ComputeDramBw(dramJob(t, nil), DramBwQuery{Spans: spans})
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary.Samples != 5 {
		t.Errorf("합산=%d want 5 (min~max 로 뭉개면 10)", r.Summary.Samples)
	}
	if r.Spans[0].Summary.Samples != 3 || *r.Spans[0].Summary.MaxMibps != 532 ||
		r.Spans[1].Summary.Samples != 2 || *r.Spans[1].Summary.MaxMibps != 500 {
		t.Errorf("spans=%+v %+v", r.Spans[0].Summary, r.Spans[1].Summary)
	}
	if len(r.Time) != 10 {
		t.Errorf("시계열은 구간을 적용하지 않는다: %d", len(r.Time))
	}
	// 뺀 가운데 구간은 수집 공백이 아니다.
	for _, w := range r.QualityWarnings {
		if strings.Contains(w, "간격") {
			t.Errorf("거짓 공백 경고: %s", w)
		}
	}
}

func TestDramAggBucketKeepsPeak(t *testing.T) {
	dir := t.TempDir()
	ev := make([]parser.DramBwEvent, 1000)
	for i := range ev {
		ev[i] = parser.DramBwEvent{Time: 10 + float64(i)*0.004, CPU: 1, Dev: "3109100.qcom,bwmon-ddr",
			Mbps: 100, Us: 4000, LineNumber: uint64(i + 1)}
	}
	ev[777].Mbps = 9000
	if err := parser.WriteDramBwParquet(ev, dir); err != nil {
		t.Fatal(err)
	}
	r, err := ComputeDramBw([]*TraceJobInfo{{Dir: dir}}, DramBwQuery{TargetPoints: 50})
	if err != nil {
		t.Fatal(err)
	}
	peak := uint32(0)
	for _, m := range r.MaxMibps {
		if m > peak {
			peak = m
		}
	}
	if !r.Bucketed || len(r.Time) < 49 || len(r.Time) > 50 || peak != 9000 {
		t.Errorf("bucketed=%v pts=%d peak=%d", r.Bucketed, len(r.Time), peak)
	}
	z, _ := ComputeDramBw([]*TraceJobInfo{{Dir: dir}}, DramBwQuery{Start: 10, End: 10 + 99*0.004 + 1e-9})
	if z.Summary.Samples != 100 {
		t.Errorf("줌 샘플=%d want 100", z.Summary.Samples)
	}
}

func TestDramAggNoParquetIsNotError(t *testing.T) {
	r, err := ComputeDramBw([]*TraceJobInfo{{Dir: t.TempDir()}}, DramBwQuery{})
	if err != nil || r.Available {
		t.Fatalf("err=%v available=%v", err, r.Available)
	}
}
