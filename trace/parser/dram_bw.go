package parser

// DRAM 대역폭 — Qualcomm `dcvs/bw_hwmon_meas` ftrace (bwmon-ddr).
//
// 정본은 Rust `../trace/src/parsers/log_common.rs::parse_dram_bw_event` 와
// `src/output/dram_bw_parquet.rs`. parquet 스키마(컬럼 이름·타입·정렬)를 같게 맞춘다 —
// 같은 dram.log 를 두 파서가 읽으면 같은 파일이 나와야 한다.
//
// 명세: `bpftrace/docs/DRAM_BW.md`.
//   - bwmon-ddr 노드만 DRAM 이다. llcc-gold/prime 은 CPU↔LLCC 구간이라 버린다
//     (더하면 같은 트래픽을 이중 계산한다, §6.1).
//   - mbps 는 이미 **MiB/s** 다. 변환하지 않는다 (§5.1).
//   - us 는 설정값 재출력이라 가중치로 쓰면 안 된다 (§3) — 원값만 싣는다.

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/parquet-go/parquet-go"
)

// DramBwEvent — bw_hwmon_meas 한 줄 (bwmon-ddr). 필드 순서·타입 = Rust DramBwEvent.
type DramBwEvent struct {
	Time       float64 `parquet:"time"`
	CPU        uint32  `parquet:"cpu"`
	Dev        string  `parquet:"dev"`
	Mbps       uint32  `parquet:"mbps"`
	Us         uint32  `parquet:"us"`
	Wake       int32   `parquet:"wake"`
	LineNumber uint64  `parquet:"line_number"`
}

const dramEventName = "bw_hwmon_meas:"

// quickDramCheck — Rust `DRAM_QUICK_CHECK`.
func quickDramCheck(line string) bool {
	return strings.Contains(line, dramEventName)
}

// isDramNode — bwmon-ddr 만 DRAM 이다.
func isDramNode(dev string) bool {
	return strings.HasSuffix(dev, "bwmon-ddr")
}

// parseDramBwLine — `dev: <name>, mbps = N, us = N, wake = N` 한 줄. 아니면 ok=false.
//
// ⚠ dev 에 콤마가 들어간다 (`3109100.qcom,bwmon-ddr`). `,` 로 나누면 깨진다 —
// `dev:` 와 **마지막** `, mbps` 사이 전체가 dev 다.
func parseDramBwLine(line string) (DramBwEvent, bool) {
	if !quickDramCheck(line) {
		return DramBwEvent{}, false
	}
	h, ok := parseFtraceHeader(line)
	if !ok {
		return DramBwEvent{}, false
	}
	payload := line[h.PayloadStart:]
	if !strings.HasPrefix(payload, dramEventName) {
		return DramBwEvent{}, false
	}
	payload = strings.TrimSpace(payload[len(dramEventName):])
	if !strings.HasPrefix(payload, "dev:") {
		return DramBwEvent{}, false
	}
	body := payload[len("dev:"):]
	mi := strings.LastIndex(body, "mbps")
	if mi < 0 {
		return DramBwEvent{}, false
	}
	devPart := strings.TrimSpace(body[:mi])
	if !strings.HasSuffix(devPart, ",") {
		return DramBwEvent{}, false
	}
	dev := strings.TrimSpace(strings.TrimSuffix(devPart, ","))
	if !isDramNode(dev) {
		return DramBwEvent{}, false
	}

	// 나머지 `mbps = N, us = N, wake = N`
	vals := map[string]string{}
	for _, kv := range strings.Split(body[mi:], ",") {
		k, v, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		vals[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	mbps, err := strconv.ParseUint(vals["mbps"], 10, 32)
	if err != nil {
		return DramBwEvent{}, false
	}
	us, _ := strconv.ParseUint(vals["us"], 10, 32)
	wake, _ := strconv.ParseInt(vals["wake"], 10, 32)
	return DramBwEvent{
		Time: h.Time,
		CPU:  h.CPU,
		Dev:  dev,
		Mbps: uint32(mbps), // ⚠ MiB/s 그대로
		Us:   uint32(us),
		Wake: int32(wake),
	}, true
}

// ParseDramLog — 파일 전체에서 bwmon-ddr 줄을 읽는다. (time, line_number) 정렬.
func ParseDramLog(path string) ([]DramBwEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var out []DramBwEvent
	var ln uint64
	for sc.Scan() {
		ln++
		if ev, ok := parseDramBwLine(sc.Text()); ok {
			ev.LineNumber = ln
			out = append(out, ev)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].LineNumber < out[j].LineNumber
	})
	return out, nil
}

// DramBwParquetName — Rust `{prefix}_dram_bw.parquet` 에 맞춘 산출물 이름.
const DramBwParquetName = "result_dram_bw.parquet"

// WriteDramBwParquet — outputDir/result_dram_bw.parquet atomic 쓰기. 빈 입력이면 파일을 안 만든다.
func WriteDramBwParquet(events []DramBwEvent, outputDir string) error {
	if len(events) == 0 {
		return nil
	}
	return writeAtomic(outputDir, DramBwParquetName, func(f *os.File) error {
		w := parquet.NewGenericWriter[DramBwEvent](f, parquet.Compression(chooseCompression(len(events))))
		if _, err := w.Write(events); err != nil {
			return err
		}
		return w.Close()
	})
}

// RunDramParquet — dram.log → result_dram_bw.parquet. 샘플 수를 돌려준다.
// 0 이면 파일을 만들지 않는다 (기존 파일은 호출자가 먼저 지운다).
func RunDramParquet(dramLog, outputDir string, progressFn ProgressFunc) (int, error) {
	events, err := ParseDramLog(dramLog)
	if err != nil {
		return 0, fmt.Errorf("parse dram log: %w", err)
	}
	if progressFn != nil {
		progressFn(fmt.Sprintf("DRAM 대역폭 샘플 %d개 (bwmon-ddr)", len(events)))
	}
	if err := WriteDramBwParquet(events, outputDir); err != nil {
		return 0, err
	}
	return len(events), nil
}
