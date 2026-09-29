package server

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"agent/trace"
	"agent/trace/parser"
)

// POST /api/agent/trace/dram-bw 를 **실제 라우트로** 태운다 — case 오타·배선은 단위 테스트로 못 잡는다.
// 응답 shape 은 portal /api/trace/dram-bw 와 같아야 한다 (UI 컴포넌트를 그대로 쓴다).
func TestDramBwRoute(t *testing.T) {
	traceDir := t.TempDir()
	withDram := filepath.Join(traceDir, "job-dram")
	noDram := filepath.Join(traceDir, "job-nodram")
	for _, d := range []string{withDram, noDram} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		// IO parquet 이 있어야 잡 디렉토리로 인식된다.
		if err := os.WriteFile(filepath.Join(d, "result_ufs.parquet"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := parser.RunDramParquet(filepath.Join("..", "trace", "parser", "testdata", "dram_bw_sample.log"), withDram, nil); err != nil {
		t.Fatal(err)
	}

	agent := NewDeviceAgentServer(nil, nil, nil, trace.NewManager(nil, t.TempDir(), traceDir), nil, nil, nil)
	mux := http.NewServeMux()
	registerRESTRoutes(mux, agent)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	post := func(body map[string]any) map[string]any {
		b, _ := json.Marshal(body)
		res, err := http.Post(srv.URL+"/api/agent/trace/dram-bw", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", res.StatusCode)
		}
		var m map[string]any
		if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	m := post(map[string]any{"jobIds": []string{"job-dram"}})
	if m["available"] != true {
		t.Fatalf("available=%v", m["available"])
	}
	s := m["summary"].(map[string]any)
	if s["samples"].(float64) != 10 || math.Abs(s["avgMibps"].(float64)-507.8) > 1e-9 || s["maxMibps"].(float64) != 1200 {
		t.Errorf("summary=%v", s)
	}
	for _, k := range []string{"time", "avgMibps", "maxMibps", "bucketed", "bucketSec", "spans", "qualityWarnings", "devs"} {
		if _, ok := m[k]; !ok {
			t.Errorf("응답에 %s 가 없다 (portal shape 불일치)", k)
		}
	}

	// 구간 두 개 → 합산 5, 구간별 3/2
	t0 := 268879.626314
	m = post(map[string]any{"jobIds": []string{"job-dram"}, "spans": []map[string]float64{
		{"start": t0 - 0.0001, "end": t0 + 0.0081}, {"start": t0 + 0.0319, "end": t0 + 0.0361},
	}})
	if m["summary"].(map[string]any)["samples"].(float64) != 5 || len(m["spans"].([]any)) != 2 {
		t.Errorf("spans 응답=%v", m["spans"])
	}

	// DRAM 이 없는 잡 — 에러가 아니라 available=false
	if m := post(map[string]any{"jobIds": []string{"job-nodram"}}); m["available"] != false {
		t.Errorf("DRAM 없는 잡: available=%v", m["available"])
	}
}
