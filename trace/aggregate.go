package trace

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	pb "agent/pb"
)

// 채팅 기반 분석용 집계 도구 레지스트리.
//
// LLM 은 SQL 을 생성하지 않는다. 아래 검증된 집계 중 **어느 것을 어떤 파라미터로 돌릴지만**
// 고르고, 실행은 이 파일의 Go 코드가 한다. 따라서 도구 선택이 틀려도 숫자 자체는 항상
// 정확하다 — 최악의 경우가 "질문과 다른 걸 계산했다" 이고, 이는 UI 의 근거 표시로 즉시
// 드러난다. text-to-SQL 의 "그럴듯한 오답" 위험이 구조적으로 없다.
//
// AggSpecs 가 단일 진실 소스다: 도구 선택 프롬프트와 실행 dispatch 가 모두 여기서
// 파생된다 (scenario.Specs 가 프롬프트·검증·UI 를 파생시키는 것과 같은 패턴).
// 드리프트는 aggregate_test.go 가 막는다.

// 집계 도구 이름. dispatch 와 프롬프트가 공유한다.
const (
	AggOverview        = "overview"
	AggTailLatency     = "tail_latency"
	AggLatencyOverTime = "latency_over_time"
	AggCmdBreakdown    = "cmd_breakdown"
	AggLatencyHist     = "latency_histogram"
	AggFilteredStats   = "filtered_stats"
	AggNone            = "none"
)

// MaxAggRows — 집계 결과 표의 행 수 상한.
//
// 두 가지를 동시에 막는다:
//  1. 근거 표시용 SSE payload 가 커지는 것
//  2. **로컬 소형 모델이 "해석" 대신 "데이터 구조 나열"로 빠지는 것** — 집계 배열이
//     길어지면 모델이 패턴을 서술하지 않고 행을 그대로 읊는다. 이 프로젝트에서 반복
//     확인된 경향이라 다른 집계 상한(rest_summary.go 의 tailN=5 / timeBins=8)도 같은
//     이유로 작게 잡혀 있다.
const MaxAggRows = 20

// AggParam — 도구가 받는 파라미터 하나. 프롬프트의 schema 와 검증이 여기서 파생된다.
type AggParam struct {
	Name     string
	Type     string // "int" | "number" | "string"
	Desc     string
	Optional bool
}

// AggSpec — 집계 도구 하나의 계약.
type AggSpec struct {
	Name    string
	Desc    string // LLM 이 도구를 고를 때 읽는 설명. 질문 예를 포함한다.
	Params  []AggParam
	NeedsDB bool // false 면 parquet 접근 없이 처리 (overview/none)
}

// AggSpecs — 도구 목록. 순서가 프롬프트 노출 순서다.
//
// none 을 반드시 포함한다: 도구 목록에 "해당 없음"이 없으면 로컬 모델이 억지로 아무
// 집계나 골라 엉뚱한 답을 만든다. job 간 비교나 무관한 질문은 여기로 떨어져야 한다.
var AggSpecs = []AggSpec{
	{
		Name:    AggOverview,
		Desc:    "이 trace 전체의 기본 통계(총 event 수, duration, read/write 비중, dtoc/ctod/ctoc/qd latency 통계, 상위 command). 질문 예: \"요약해줘\", \"전반적으로 어때?\"",
		NeedsDB: false,
	},
	{
		Name: AggTailLatency,
		Desc: "dtoc 가 가장 큰 느린 request 상위 N개를 시각·command·size·QD 와 함께 나열. 질문 예: \"제일 느린 IO 10개\", \"어떤 request 가 튀었어?\"",
		Params: []AggParam{
			{Name: "n", Type: "int", Desc: fmt.Sprintf("가져올 개수 (1~%d, 기본 10)", MaxAggRows), Optional: true},
		},
		NeedsDB: true,
	},
	{
		Name: AggLatencyOverTime,
		Desc: "전체 구간을 시간 bin 으로 나눠 bin 별 event 수와 avg/p99 dtoc 추이. 질문 예: \"언제 느려졌어?\", \"latency 가 특정 구간에 몰렸나?\"",
		Params: []AggParam{
			{Name: "bins", Type: "int", Desc: fmt.Sprintf("나눌 구간 수 (2~%d, 기본 12)", MaxAggRows), Optional: true},
		},
		NeedsDB: true,
	},
	{
		Name:    AggCmdBreakdown,
		Desc:    "command(UFS opcode / Block io_type) 별 count·비중·총 bytes·dtoc 통계. 질문 예: \"read/write 비중이 어떻게 돼?\", \"어떤 opcode 가 많아?\"",
		NeedsDB: true,
	},
	{
		Name: AggLatencyHist,
		Desc: "latency 분포 histogram (구간별 request 수). 질문 예: \"dtoc 분포 보여줘\", \"몇 ms 대에 몰려 있어?\"",
		Params: []AggParam{
			{Name: "column", Type: "string", Desc: "dtoc | ctod | ctoc (기본 dtoc)", Optional: true},
		},
		NeedsDB: true,
	},
	{
		Name: AggFilteredStats,
		Desc: "특정 조건(시간 구간 / command)으로 좁혀 통계를 내고 **전체 기준선과 나란히** 비교. 질문 예: \"180~190초 구간만 보면?\", \"write 만 놓고 보면 어때?\"",
		Params: []AggParam{
			{Name: "start_time", Type: "number", Desc: "시작 시각(초). 시간으로 좁힐 때만", Optional: true},
			{Name: "end_time", Type: "number", Desc: "끝 시각(초). 시간으로 좁힐 때만", Optional: true},
			{Name: "cmd", Type: "string", Desc: "command 하나로 좁힐 때. UFS 는 opcode(0x28=READ, 0x2A=WRITE, 0x35=SYNC, 0x42=DISCARD), Block 은 io_type", Optional: true},
		},
		NeedsDB: true,
	},
	{
		Name:    AggNone,
		Desc:    "이 trace 의 데이터만으로는 답할 수 없는 질문. 다른 job 과의 비교(\"지난주보다 나쁜가\"), 저장되지 않은 정보, trace 와 무관한 질문이 여기 해당한다. 억지로 다른 집계를 고르지 말고 반드시 이것을 고를 것.",
		NeedsDB: false,
	},
}

// AggSpecByName — 이름으로 spec 조회.
func AggSpecByName(name string) (AggSpec, bool) {
	for _, s := range AggSpecs {
		if s.Name == name {
			return s, true
		}
	}
	return AggSpec{}, false
}

// AggNames — 도구 이름 목록 (프롬프트 enum 용).
func AggNames() []string {
	out := make([]string, 0, len(AggSpecs))
	for _, s := range AggSpecs {
		out = append(out, s.Name)
	}
	return out
}

// AggToolReference — 도구 목록을 프롬프트에 넣을 텍스트로 조립한다.
// AggSpecs 에서 파생되므로 도구를 추가하면 프롬프트가 자동으로 따라온다.
func AggToolReference() string {
	var b strings.Builder
	for _, s := range AggSpecs {
		b.WriteString("- ")
		b.WriteString(s.Name)
		b.WriteString(": ")
		b.WriteString(s.Desc)
		if len(s.Params) > 0 {
			b.WriteString("\n  params: ")
			parts := make([]string, 0, len(s.Params))
			for _, p := range s.Params {
				opt := ""
				if p.Optional {
					opt = ", 선택"
				}
				parts = append(parts, fmt.Sprintf("%s(%s%s) — %s", p.Name, p.Type, opt, p.Desc))
			}
			b.WriteString(strings.Join(parts, "; "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// AggResult — 집계 실행 결과. UI 근거 표시와 LLM 주입에 함께 쓰인다.
type AggResult struct {
	Tool      string         `json:"tool"`
	Params    map[string]any `json:"params,omitempty"`
	Query     string         `json:"query,omitempty"`     // 사용자에게 노출하는 근거 (실행한 SQL 또는 집계 설명)
	RowCount  int            `json:"rowCount"`            // 잘리기 전 실제 행 수
	Truncated bool           `json:"truncated,omitempty"` // MaxAggRows 로 잘렸는지
	Data      map[string]any `json:"data,omitempty"`      // 결과 본문
	Note      string         `json:"note,omitempty"`      // 실행 불가/부분 실패 사유
}

// RunAggregation — 도구 하나를 실행한다.
//
// overview / none 은 parquet 접근이 없으므로 호출자가 처리하고 여기 오지 않는다
// (NeedsDB=false). 나머지는 **커넥션 하나를 열어 필요한 쿼리만** 돌린다 — ComputeStats
// 통짜 호출은 같은 parquet 을 10회 이상 재스캔하므로 턴마다 부르면 비싸다.
func RunAggregation(infos []*TraceJobInfo, tool string, params map[string]any) (*AggResult, error) {
	spec, ok := AggSpecByName(tool)
	if !ok {
		return nil, fmt.Errorf("알 수 없는 집계 도구: %s", tool)
	}
	res := &AggResult{Tool: tool, Params: params, Data: map[string]any{}}

	// parquet 이 하나도 없으면 buildGlobList 가 "''" 를 반환해 DuckDB 쿼리 에러가 난다
	// (빈 결과가 아니라). LLM 이 에러 문자열을 해석하지 않도록 여기서 명확히 끊는다.
	if !hasParquet(infos) {
		return nil, fmt.Errorf("이 job 의 parquet 결과 파일을 찾을 수 없습니다")
	}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	glob := buildGlobList(infos)
	cmdCol := detectCmdColumn(db, glob)
	lbaCol := detectLbaColumn(db, glob)
	timeCol := detectTimeColumn(db, glob)

	switch spec.Name {
	case AggTailLatency:
		n := clampInt(paramInt(params, "n", 10), 1, MaxAggRows)
		events, err := queryTailLatency(db, glob, "", cmdCol, timeCol, n)
		if err != nil {
			return nil, fmt.Errorf("tail latency 집계 실패: %w", err)
		}
		res.Data["events"] = events
		res.RowCount = len(events)
		res.Query = fmt.Sprintf("SELECT %s AS time, %s AS cmd, size, dtoc, qd\nFROM read_parquet(...)\nWHERE dtoc > 0\nORDER BY dtoc DESC LIMIT %d",
			orNull(timeCol), cmdCol, n)

	case AggLatencyOverTime:
		if timeCol == "" {
			res.Note = "이 trace 에는 시간 컬럼이 없어 구간별 추이를 낼 수 없습니다"
			return res, nil
		}
		bins := clampInt(paramInt(params, "bins", 12), 2, MaxAggRows)
		series, err := queryTimeSeriesLatency(db, glob, "", timeCol, bins)
		if err != nil {
			return nil, fmt.Errorf("구간별 latency 집계 실패: %w", err)
		}
		res.Data["bins"] = series
		res.RowCount = len(series)
		res.Query = fmt.Sprintf("SELECT bin, count(*), avg(dtoc), percentile_cont(0.99) ... \nFROM read_parquet(...)\nWHERE dtoc > 0\nGROUP BY floor(%s / binWidth)  -- bins=%d", timeCol, bins)

	case AggCmdBreakdown:
		cmds, err := queryCmdStats(db, glob, "")
		if err != nil {
			return nil, fmt.Errorf("command 집계 실패: %w", err)
		}
		res.RowCount = len(cmds)
		if len(cmds) > MaxAggRows {
			cmds = cmds[:MaxAggRows]
			res.Truncated = true
		}
		res.Data["commands"] = cmdStatsToRows(cmds)
		res.Query = fmt.Sprintf("SELECT %s AS cmd, count(*), sum(size), avg(dtoc), percentile_cont(0.99) ...\nFROM read_parquet(...)\nGROUP BY cmd ORDER BY count(*) DESC", cmdCol)

	case AggLatencyHist:
		col := paramLatencyCol(params)
		hists, err := queryLatencyHistograms(db, glob, "", col, defaultLatencyRanges)
		if err != nil {
			return nil, fmt.Errorf("histogram 집계 실패: %w", err)
		}
		rows := histogramTotals(hists)
		res.Data["column"] = col
		res.Data["buckets"] = rows
		res.RowCount = len(rows)
		res.Query = fmt.Sprintf("SELECT bucket(%s), count(*)\nFROM read_parquet(...)\nGROUP BY bucket  -- ranges(ms): %v", col, defaultLatencyRanges)

	case AggFilteredStats:
		filter, desc, problem := buildAggFilter(params)
		if problem != "" {
			res.Note = problem
			return res, nil
		}
		if filter == nil {
			res.Note = "좁힐 조건(start_time/end_time/cmd)이 지정되지 않았습니다"
			return res, nil
		}
		where := buildFilterWhere(filter, lbaCol, cmdCol)
		scoped, err := querySliceSummary(db, glob, where, cmdCol)
		if err != nil {
			return nil, fmt.Errorf("구간 집계 실패: %w", err)
		}
		// 기준선(전체) — 필터 없이 같은 쿼리. 비교 없이는 "write 71.3%" 같은 수치가
		// 높은지 낮은지 판단할 수 없다.
		baseline, err := querySliceSummary(db, glob, "", cmdCol)
		if err != nil {
			return nil, fmt.Errorf("기준선 집계 실패: %w", err)
		}
		res.Data["filter"] = desc
		res.Data["scoped"] = scoped
		res.Data["overall"] = baseline
		res.RowCount = 1
		res.Query = fmt.Sprintf("-- 좁힌 구간\nSELECT count(*), avg(dtoc), percentile_cont(0.99), write 비중 ...\nFROM read_parquet(...) %s\n\n-- 전체 기준선 (같은 쿼리, 조건 없음)", where)

	default:
		return nil, fmt.Errorf("집계 %s 는 parquet 실행 대상이 아닙니다", spec.Name)
	}

	return res, nil
}

// hasParquet — infos 중 실제 parquet 파일이 하나라도 있는지.
func hasParquet(infos []*TraceJobInfo) bool {
	for _, info := range infos {
		if len(findParquetFiles(info.Dir, info.TraceType)) > 0 {
			return true
		}
	}
	return false
}

// querySliceSummary — 한 구간(또는 전체)의 요약 지표. filtered_stats 가 좁힌 구간과
// 기준선에 같은 쿼리를 써서 나란히 비교할 수 있게 한다.
func querySliceSummary(db *sql.DB, glob, where, cmdCol string) (map[string]any, error) {
	q := fmt.Sprintf(`SELECT
		count(*) AS cnt,
		avg(CASE WHEN dtoc > 0 THEN dtoc END) AS avg_dtoc,
		percentile_cont(0.99) WITHIN GROUP (ORDER BY CASE WHEN dtoc > 0 THEN dtoc END) AS p99_dtoc,
		max(dtoc) AS max_dtoc,
		avg(qd) AS avg_qd,
		sum(CASE WHEN lower(CAST(%s AS VARCHAR)) IN ('0x2a', '0x8a')
		          OR lower(CAST(%s AS VARCHAR)) LIKE 'w%%' THEN 1 ELSE 0 END) AS write_cnt
	FROM read_parquet(%s) %s`, cmdCol, cmdCol, glob, where)

	var cnt, writeCnt sql.NullInt64
	var avgD, p99D, maxD, avgQ sql.NullFloat64
	if err := db.QueryRow(q).Scan(&cnt, &avgD, &p99D, &maxD, &avgQ, &writeCnt); err != nil {
		return nil, err
	}
	out := map[string]any{
		"events":  cnt.Int64,
		"avgDtoc": round3(avgD.Float64),
		"p99Dtoc": round3(p99D.Float64),
		"maxDtoc": round3(maxD.Float64),
		"avgQd":   round3(avgQ.Float64),
	}
	if cnt.Int64 > 0 {
		out["writeRatio"] = round3(float64(writeCnt.Int64) / float64(cnt.Int64))
	}
	return out, nil
}

// buildAggFilter — 도구 params 를 TraceFilter 로 변환.
//
// 반환: (filter, 설명, 문제사유). filter 가 nil 이면 좁힐 조건이 없다는 뜻이고,
// 이때 문제사유가 비어있지 않으면 **모델이 잘못된 값을 넣었다**는 뜻이다.
//
// 자리표시자를 조용히 무시하면 안 된다 — "구간을 좁혀 계산했다"는 근거 뱃지가 뜨는데
// 실제로는 전체를 계산한 것이 되어 사용자가 틀린 줄 모른다.
func buildAggFilter(params map[string]any) (*pb.TraceFilter, string, string) {
	f := &pb.TraceFilter{}
	var parts []string
	var bad []string

	// 자리표시자 먼저 검사 (숫자 파싱 실패와 구분하기 위해).
	for _, key := range []string{"start_time", "end_time", "cmd"} {
		if raw := paramString(params, key); raw != "" && isPlaceholder(raw) {
			bad = append(bad, fmt.Sprintf("%s=%q", key, raw))
		}
	}
	if len(bad) > 0 {
		return nil, "", fmt.Sprintf("실제 값 대신 자리표시자가 지정되어 집계를 실행하지 않았습니다 (%s)", strings.Join(bad, ", "))
	}

	if v, ok := paramFloat(params, "start_time"); ok && v > 0 {
		f.StartTime = v
		parts = append(parts, fmt.Sprintf("time >= %g", v))
	}
	if v, ok := paramFloat(params, "end_time"); ok && v > 0 {
		f.EndTime = v
		parts = append(parts, fmt.Sprintf("time <= %g", v))
	}
	if s := strings.TrimSpace(paramString(params, "cmd")); s != "" {
		// opcode 는 parquet 에 소문자로 저장된다("0x2a"). 모델은 도메인 관례대로 대문자
		// ("0x2A")를 내기 쉬운데, buildFilterWhere 는 대소문자를 구분해 IN 비교하므로
		// 그대로 두면 **0건이 매칭돼 조용히 틀린 답**이 나온다.
		// 양쪽 표기를 모두 넣어 어느 쪽이든 잡히게 한다.
		f.CmdList = cmdCaseVariants(s)
		parts = append(parts, "cmd = "+s)
	}
	if len(parts) == 0 {
		return nil, "", ""
	}
	return f, strings.Join(parts, ", "), ""
}

// cmdCaseVariants — command 값의 대소문자 변형을 함께 반환한다.
// 0x 접두 opcode 는 물론 Block 의 io_type("R"/"w" 등)도 표기가 섞일 수 있다.
func cmdCaseVariants(s string) []string {
	lower := strings.ToLower(s)
	upper := strings.ToUpper(s)
	// 0x 접두는 접두만 소문자로 유지하는 표기가 흔하다(0x2A).
	mixed := ""
	if strings.HasPrefix(lower, "0x") && len(s) > 2 {
		mixed = "0x" + strings.ToUpper(s[2:])
	}
	seen := map[string]bool{}
	var out []string
	for _, v := range []string{s, lower, upper, mixed} {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// cmdStatsToRows — pb.CmdStats 를 근거 표시용 평면 맵으로. 필드가 많아 핵심만 추린다.
func cmdStatsToRows(cmds []*pb.CmdStats) []map[string]any {
	out := make([]map[string]any, 0, len(cmds))
	for _, c := range cmds {
		row := map[string]any{
			"cmd":            c.GetCmd(),
			"count":          c.GetCount(),
			"ratio":          round3(c.GetRatio()),
			"totalSizeBytes": c.GetTotalSizeBytes(),
		}
		if d := c.GetDtoc(); d != nil {
			row["dtocAvg"] = round3(d.GetAvg())
			row["dtocP99"] = round3(d.GetP99())
			row["dtocMax"] = round3(d.GetMax())
		}
		out = append(out, row)
	}
	return out
}

// histogramTotals — cmd 별로 쪼개진 histogram 을 버킷 단위로 합산해 전체 분포 하나로 만든다.
// LLM 에는 cmd × bucket 전체보다 합산 분포가 읽기 쉽다.
//
// pb.LatencyBucket 에 id 가 없으므로 range_start_ms 로 버킷을 동일시한다
// (queryLatencyHistograms 가 모든 cmd 에 같은 ranges 를 쓰므로 경계값이 일치한다).
func histogramTotals(hists []*pb.LatencyHistogram) []map[string]any {
	type agg struct {
		from, to float64
		count    int64
	}
	byStart := map[float64]*agg{}
	var starts []float64
	for _, h := range hists {
		for _, b := range h.GetBuckets() {
			k := b.GetRangeStartMs()
			if _, ok := byStart[k]; !ok {
				byStart[k] = &agg{from: k, to: b.GetRangeEndMs()}
				starts = append(starts, k)
			}
			byStart[k].count += b.GetCount()
		}
	}
	sort.Float64s(starts)
	out := make([]map[string]any, 0, len(starts))
	for _, k := range starts {
		a := byStart[k]
		out = append(out, map[string]any{
			"fromMs": a.from,
			"toMs":   a.to,
			"count":  a.count,
		})
	}
	return out
}

// ==================== param 헬퍼 ====================
//
// LLM 이 schema 로 강제돼도 숫자를 문자열로 내는 경우가 있어 방어적으로 변환한다.

func paramInt(p map[string]any, key string, def int) int {
	if v, ok := paramFloat(p, key); ok {
		return int(v)
	}
	return def
}

func paramFloat(p map[string]any, key string) (float64, bool) {
	if p == nil {
		return 0, false
	}
	switch v := p[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		return parseNumericParam(v)
	}
	return 0, false
}

// numberRe — 문자열에서 숫자 후보를 뽑는다(음수/소수 포함).
var numberRe = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

// parseNumericParam — 모델이 낸 문자열에서 숫자를 얻는다.
//
// 우선 전체가 숫자인지 본다(정상 경로). 아니면 문자열 안에 숫자가 **정확히 하나**일
// 때만 그것을 취한다 — 모델이 값을 그대로 주지 않고 설명을 붙이는 경우가 있다
// (예: "With the actual number from the question, it should be: 947257").
//
// 숫자가 여럿이면 어느 것이 값인지 알 수 없으므로 실패로 둔다. 아무거나 고르면
// 조용히 틀린 구간을 계산하게 되고, 근거 뱃지는 정상처럼 보인다.
func parseNumericParam(v string) (float64, bool) {
	t := strings.TrimSpace(v)
	if t == "" {
		return 0, false
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil {
		return f, true
	}
	m := numberRe.FindAllString(t, -1)
	if len(m) != 1 {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[0], 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// isPlaceholder — 모델이 실제 값 대신 넣는 자리표시자 판별.
//
// 실측: 후속 질문에서 14b 가 start_time 에 "value_from_previous_question" 을 넣었다.
// 이런 값이 필터에 조용히 무시되면 "구간을 좁혀 계산했다"는 근거 뱃지가 뜨는데 실제로는
// 전체를 계산한 셈이 되어, 사용자가 틀린 줄 모른다. 명시적으로 걸러 안내한다.
func isPlaceholder(v string) bool {
	t := strings.ToLower(strings.TrimSpace(v))
	if t == "" {
		return false
	}
	// 숫자를 하나 확정할 수 있으면 자리표시자가 아니다(설명이 붙어 있어도 값은 쓸 수 있다).
	if _, ok := parseNumericParam(t); ok {
		return false
	}
	for _, pat := range []string{
		"previous", "이전", "same_as", "same as", "above", "위의", "value_from",
		"unknown", "n/a", "null", "todo", "placeholder", "<", "{",
	} {
		if strings.Contains(t, pat) {
			return true
		}
	}
	return false
}

func paramString(p map[string]any, key string) string {
	if p == nil {
		return ""
	}
	if s, ok := p[key].(string); ok {
		return s
	}
	return ""
}

// paramLatencyCol — histogram 대상 컬럼. 화이트리스트로만 받는다(SQL 조립에 들어가므로).
func paramLatencyCol(p map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(paramString(p, "column"))) {
	case "ctod":
		return "ctod"
	case "ctoc":
		return "ctoc"
	default:
		return "dtoc"
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func orNull(col string) string {
	if col == "" {
		return "NULL"
	}
	return col
}

func round3(f float64) float64 {
	return float64(int64(f*1000+0.5)) / 1000
}
