package parser

import (
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
)

// fixture 는 Rust `../trace/tests/fixtures/dram_bw_sample.log` 의 사본이다.
// bwmon-ddr 10줄(mbps = dramVals) + llcc-gold/prime 각 10줄 + ufshcd 2줄 + block 2줄.
// ddr 한 줄은 comm 에 `block_` 과 `[]` 가 들어 있다.
var dramVals = []uint32{532, 469, 300, 610, 1, 1200, 800, 400, 266, 500}

func TestDramDevWithCommaKeptWhole(t *testing.T) {
	ev, ok := parseDramBwLine("     kworker/5:2H-25770  [005] ..... 268879.626314: bw_hwmon_meas: dev: 3109100.qcom,bwmon-ddr, mbps = 532, us = 4000, wake = 0")
	if !ok {
		t.Fatal("ddr 줄을 못 읽었다")
	}
	if ev.Dev != "3109100.qcom,bwmon-ddr" || ev.Mbps != 532 || ev.Us != 4000 || ev.CPU != 5 {
		t.Fatalf("got %+v", ev)
	}
}

func TestDramLlccDropped(t *testing.T) {
	for _, dev := range []string{"310b3300.qcom,bwmon-llcc-gold", "310b7300.qcom,bwmon-llcc-prime"} {
		if _, ok := parseDramBwLine("  kworker/5:2H-1  [005] ..... 1.000000: bw_hwmon_meas: dev: " + dev + ", mbps = 9999, us = 4000, wake = 0"); ok {
			t.Errorf("%s 는 버려야 한다 (DRAM 이 아니다, 더하면 이중 계산)", dev)
		}
	}
}

func TestDramBracketCommAndNegativeWake(t *testing.T) {
	ev, ok := parseDramBwLine("  kworker/block_x[1]-99  [005] ..... 2.000000: bw_hwmon_meas: dev: 3109100.qcom,bwmon-ddr, mbps = 1200, us = 4000, wake = -1")
	if !ok || ev.Mbps != 1200 || ev.Wake != -1 {
		t.Fatalf("got %+v ok=%v", ev, ok)
	}
}

func TestDramFixture(t *testing.T) {
	events, err := ParseDramLog(filepath.Join("testdata", "dram_bw_sample.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != len(dramVals) {
		t.Fatalf("샘플 %d개, want %d", len(events), len(dramVals))
	}
	for i, e := range events {
		// mbps 는 MiB/s 그대로 — 1.048576 배 같은 변환이 끼면 안 된다.
		if e.Mbps != dramVals[i] {
			t.Errorf("[%d] mbps=%d want %d", i, e.Mbps, dramVals[i])
		}
	}
	if events[0].LineNumber != 3 {
		t.Errorf("line_number=%d want 3 (헤더 2줄 다음)", events[0].LineNumber)
	}

	dir := t.TempDir()
	if _, err := RunDramParquet(filepath.Join("testdata", "dram_bw_sample.log"), dir, nil); err != nil {
		t.Fatal(err)
	}
	got, err := parquet.ReadFile[DramBwEvent](filepath.Join(dir, DramBwParquetName))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(dramVals) || got[5].Mbps != 1200 {
		t.Fatalf("parquet 왕복 불일치: %+v", got)
	}
}
