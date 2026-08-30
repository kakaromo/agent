package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"agent/trace"
)

// POST /api/agent/trace/fsio-read-stats 를 **실제 라우트로** 태운다.
//
// 변환 함수 단위 테스트만으로는 라우팅·핸들러 배선이 검증되지 않는다 —
// case 문에 오타가 있어도 단위 테스트는 통과한다.
//
// parquet fixture 가 있으면 실제 숫자까지 본다:
//
//	go run ./cmd/goparse <fsio 로그> <dir> fsio_ufs
//	FSIO_READ_FIXTURE_DIR=<dir> go test ./server/ -run FsioReadStatsRoute
func TestFsioReadStatsRoute(t *testing.T) {
	traceDir := t.TempDir()
	jobID := "job-fsio-read"
	jobDir := filepath.Join(traceDir, jobID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// fixture 가 있으면 복사해 실제 집계까지, 없으면 빈 잡으로 계약만 본다.
	fixture := os.Getenv("FSIO_READ_FIXTURE_DIR")
	haveData := false
	if fixture != "" {
		src := filepath.Join(fixture, "result_fsio_read.parquet")
		if b, err := os.ReadFile(src); err == nil {
			if err := os.WriteFile(filepath.Join(jobDir, "result_fsio_read.parquet"), b, 0o644); err != nil {
				t.Fatal(err)
			}
			haveData = true
		}
	}
	if !haveData {
		// parquet 이 하나도 없으면 GetTraceJobInfo 가 디렉토리를 잡지 못한다.
		// 형제가 없는 잡(= Page Cache 를 못 보여주는 잡)을 흉내내기 위해 다른 타입을 둔다.
		if err := os.WriteFile(filepath.Join(jobDir, "result_fsio_ufs.parquet"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	agent := NewDeviceAgentServer(nil, nil, nil, trace.NewManager(nil, t.TempDir(), traceDir), nil, nil, nil)
	mux := http.NewServeMux()
	registerRESTRoutes(mux, agent)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{"jobIds": []string{jobID}, "topN": 5})
	res, err := http.Post(srv.URL+"/api/agent/trace/fsio-read-stats", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	// 형제 parquet 이 없어도 **200** 이어야 한다 — 에러면 정상 job 에 빨간 배너가 뜬다.
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (형제 parquet 유무와 무관)", res.StatusCode)
	}
	var m map[string]any
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m["schemaVersion"] != "fsio_read-v1" {
		t.Errorf("schemaVersion = %v", m["schemaVersion"])
	}

	if !haveData {
		if m["totalRequests"].(float64) != 0 {
			t.Errorf("형제 parquet 이 없으면 totalRequests=0 이어야 한다: %v", m["totalRequests"])
		}
		t.Log("fixture 없음 — 계약만 확인 (FSIO_READ_FIXTURE_DIR 로 실데이터 검증 가능)")
		return
	}

	// 실데이터 — Rust 대조로 확인한 값들.
	if got := m["totalRequests"].(float64); got != 363 {
		t.Errorf("totalRequests = %v, want 363", got)
	}
	hit, ok := m["requestHitRatio"].(float64)
	if !ok {
		t.Fatal("requestHitRatio 가 없다 — 판정 대상이 있으면 실려야 한다")
	}
	// hit 232 / (232+97) = 0.70517
	if hit < 0.705 || hit > 0.706 {
		t.Errorf("requestHitRatio = %v, want ≈0.7052", hit)
	}
	// coverage 안 되는 행이 있으므로 경고가 반드시 있어야 한다 (숨기지 않는다).
	if w, _ := m["qualityWarnings"].([]any); len(w) == 0 {
		t.Error("UNKNOWN 행이 있는데 품질 경고가 비었다")
	}
	// hit 은 miss 보다 빨라야 한다 — 이 기능이 존재하는 이유다.
	var hitAvg, missAvg float64
	for _, c := range m["byClass"].([]any) {
		cm := c.(map[string]any)
		if v, ok := cm["durationAvgNs"].(float64); ok {
			switch cm["cacheClass"] {
			case "CACHE_HIT_INFERRED":
				hitAvg = v
			case "CACHE_MISS":
				missAvg = v
			}
		}
	}
	if hitAvg == 0 || missAvg == 0 {
		t.Fatalf("지연 평균이 비었다 (hit=%v miss=%v) — duration_ns 감지 실패", hitAvg, missAvg)
	}
	if hitAvg >= missAvg {
		t.Errorf("hit 평균(%v) 이 miss(%v) 보다 느리다 — 분류가 뒤집혔을 수 있다", hitAvg, missAvg)
	}
}
