package trace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// 업로드된 파일의 포맷 자동 판별.
//
// **왜 자동인가.** 사용자가 trace_type 을 잘못 고르면 파서가 아무 행도 못 만들고
// **에러 없이 0건**으로 끝난다 — "수집은 됐는데 비어 있다" 로 보여 원인 추적이
// 어렵다. 그래서 파일 내용으로 정하고, 못 정하면 **명확히 실패**시킨다.
//
// 판별은 파서가 실제로 거르는 조건과 같은 근거를 쓴다:
//   - fsio  : TAB 17컬럼 + 1번째가 실수(time) + 2번째가 레이어 이름
//     (parser.splitFsioCols 와 같은 판정)
//   - ftrace: 공백 구분에 `ufshcd_command:` / `block_rq_*` 이벤트 이름
//   - bench : JSON 객체에 deviceId + tool
const (
	// UploadKindTrace — trace 로그 (ftrace 또는 fsio TSV).
	UploadKindTrace = "trace"
	// UploadKindBenchmark — 벤치마크 결과 JSON.
	UploadKindBenchmark = "benchmark"
)

// DetectResult — 판별 결과.
type DetectResult struct {
	// Kind: UploadKindTrace | UploadKindBenchmark
	Kind string
	// TraceType: Kind==trace 일 때만. "ufs"|"block"|"both"|"fsio_ufs"|"fsio_block"
	TraceType string
	// Reason: 무엇을 보고 그렇게 판단했는지 (사용자에게 보여준다).
	Reason string
}

// detectScanLines — 판별에 쓸 최대 줄 수.
//
// 앞부분에 헤더/빈 줄이 섞일 수 있어 한 줄만 보면 안 된다. 반대로 너무 많이 읽으면
// 대용량 업로드에서 판별만으로 시간을 쓴다.
const detectScanLines = 200

// DetectUpload — 업로드 파일의 앞부분을 읽어 포맷을 판별한다.
//
// r 은 파일 선두부터 읽을 수 있어야 한다(호출부가 되감거나 별도 리더를 준다).
func DetectUpload(r io.Reader, filename string) (DetectResult, error) {
	br := bufio.NewReaderSize(r, 1<<20)

	// JSON 은 첫 비공백 문자가 '{' 다 — 줄 단위로 보기 전에 먼저 가른다.
	if b, err := peekFirstNonSpace(br); err == nil && b == '{' {
		return detectJSON(br, filename)
	}

	sc := bufio.NewScanner(br)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20) // fsio 한 줄이 길 수 있다

	var (
		fsioUFS, fsioBlock   int
		ftraceUFS, ftraceBlk int
		seen                 int
	)
	for sc.Scan() && seen < detectScanLines {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		seen++

		if layer, ok := fsioLayerOf(line); ok {
			switch layer {
			case "UFS":
				fsioUFS++
			case "BLK":
				fsioBlock++
			}
			// VFS/FS 행은 어느 쪽에도 표를 주지 않는다 — 두 계열 모두에 섞여 나온다.
			continue
		}
		if strings.Contains(line, "ufshcd_command:") {
			ftraceUFS++
		} else if strings.Contains(line, "block_rq_issue") || strings.Contains(line, "block_rq_complete") {
			ftraceBlk++
		}
	}
	if err := sc.Err(); err != nil {
		return DetectResult{}, fmt.Errorf("파일 읽기 실패: %w", err)
	}

	switch {
	case fsioUFS > 0 && fsioBlock > 0:
		// 한 파일에 둘 다 — fsiotrace 는 --only 로 한 계열만 내보내므로 정상은 아니다.
		return DetectResult{}, fmt.Errorf(
			"fsio 로그에 UFS 와 BLK 가 섞여 있습니다 (UFS %d행, BLK %d행). "+
				"수집 시 --only 로 한 계열만 받아야 합니다", fsioUFS, fsioBlock)
	case fsioUFS > 0:
		return DetectResult{Kind: UploadKindTrace, TraceType: "fsio_ufs",
			Reason: fmt.Sprintf("fsio TSV (UFS 행 %d개)", fsioUFS)}, nil
	case fsioBlock > 0:
		return DetectResult{Kind: UploadKindTrace, TraceType: "fsio_block",
			Reason: fmt.Sprintf("fsio TSV (BLK 행 %d개)", fsioBlock)}, nil
	case ftraceUFS > 0 && ftraceBlk > 0:
		return DetectResult{Kind: UploadKindTrace, TraceType: "both",
			Reason: fmt.Sprintf("ftrace (ufshcd_command %d개 + block_rq %d개)", ftraceUFS, ftraceBlk)}, nil
	case ftraceUFS > 0:
		return DetectResult{Kind: UploadKindTrace, TraceType: "ufs",
			Reason: fmt.Sprintf("ftrace (ufshcd_command %d개)", ftraceUFS)}, nil
	case ftraceBlk > 0:
		return DetectResult{Kind: UploadKindTrace, TraceType: "block",
			Reason: fmt.Sprintf("ftrace (block_rq %d개)", ftraceBlk)}, nil
	}

	// 여기까지 왔으면 아는 포맷이 아니다. **추측해서 진행하지 않는다** —
	// 잘못 고르면 산출물이 조용히 0건이 된다.
	return DetectResult{}, fmt.Errorf(
		"포맷을 알 수 없습니다 (%s, 앞 %d줄 검사). "+
			"지원: ftrace 로그(ufshcd_command / block_rq_*), fsio TSV(17컬럼), 벤치마크 결과 JSON",
		filename, seen)
}

// fsioLayerOf — fsio TSV 한 줄이면 layer 를 돌려준다.
//
// parser.splitFsioCols 와 같은 판정(17컬럼 + 1번째가 실수)에 레이어 이름 검사를
// 더한 것이다. 판정을 느슨하게 하면 다른 TAB 파일이 fsio 로 오인된다.
func fsioLayerOf(line string) (string, bool) {
	cols := strings.Split(line, "\t")
	if len(cols) < 17 {
		return "", false
	}
	if _, err := strconv.ParseFloat(cols[0], 64); err != nil {
		return "", false
	}
	switch cols[1] {
	case "VFS", "FS", "BLK", "UFS":
		return cols[1], true
	}
	return "", false
}

// detectJSON — JSON 업로드가 벤치마크 결과인지 확인한다.
func detectJSON(r io.Reader, filename string) (DetectResult, error) {
	var probe map[string]json.RawMessage
	if err := json.NewDecoder(r).Decode(&probe); err != nil {
		return DetectResult{}, fmt.Errorf("JSON 파싱 실패: %w", err)
	}
	_, hasDevice := probe["deviceId"]
	_, hasTool := probe["tool"]
	if hasDevice && hasTool {
		return DetectResult{Kind: UploadKindBenchmark,
			Reason: "벤치마크 결과 JSON (deviceId + tool)"}, nil
	}
	// 시나리오 export 등 다른 JSON 을 벤치마크로 넣으면 화면이 이상해진다 — 거부한다.
	return DetectResult{}, fmt.Errorf(
		"벤치마크 결과 JSON 이 아닙니다 (%s). deviceId 와 tool 필드가 필요합니다. "+
			"시나리오 JSON 이면 Scenario 탭의 import 를 쓰세요", filename)
}

// peekFirstNonSpace — 앞쪽 공백을 건너뛰고 첫 문자를 확인한다(소비하지 않는다).
func peekFirstNonSpace(br *bufio.Reader) (byte, error) {
	for i := 0; i < 64; i++ {
		b, err := br.Peek(i + 1)
		if err != nil {
			return 0, err
		}
		c := b[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		return c, nil
	}
	return 0, fmt.Errorf("선두 공백이 너무 길다")
}
